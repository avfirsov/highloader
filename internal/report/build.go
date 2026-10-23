package report

import (
	"fmt"
	"highloader/internal/engine"
	"highloader/internal/history"
	"sort"
	"strconv"
)

func Build(h history.Report, o Options) (RunReport, error) {
	r := RunReport{SchemaVersion: 1, ID: h.RunID, Snapshot: h.Snapshot, State: h.State, Reason: h.Reason, DurableCut: h.DurableCut, Durable: true, Recovery: h.Recovery, Templates: []DimensionSummary{}, Stages: []DimensionSummary{}, Points: []ChartPoint{}, Samples: []engine.Sample{}, Population: o.Population, TemplateID: o.TemplateID}
	if o.FromIndex < 0 || o.Limit < 0 || o.SampleOffset < 0 || o.SampleLimit < 0 {
		return r, fmt.Errorf("invalid report range")
	}
	r.Revision = decimal(h.LatestSequence)
	r.DurableRevision = r.Revision
	if h.Checkpoint != nil {
		r.Revision = decimal(h.Checkpoint.Sequence)
	}
	if o.Population == "http" || o.Population == "technical" {
		r.LogicalPopulationNote = "Для логических запросов причина HTTP/технической ошибки не классифицируется; задержка этой популяции недоступна."
	}
	var err error
	r.Summary, err = summarize(engine.Aggregate{}, o.Population)
	if err != nil {
		return r, err
	}
	if h.Checkpoint != nil {
		r.Cut = h.Checkpoint.Cut
		// equal coordinator timestamps do not acknowledge a newer revision.
		r.Durable = h.Checkpoint.Sequence <= h.LatestSequence && h.DurableCut != nil && !r.Cut.After(*h.DurableCut)
	}
	found := o.TemplateID == ""
	for _, d := range h.Aggregates {
		s, e := summarize(d.Aggregate, o.Population)
		if e != nil {
			return r, e
		}
		label := d.TemplateID
		if d.Kind == "stage" {
			label = strconv.Itoa(d.Stage)
		}
		if h.Recovery != nil {
			for _, u := range h.Recovery.Unknown {
				if u.OutcomeCommitted {
					continue
				}
				if d.Kind == "overall" || d.Kind == "template" && d.TemplateID == u.TemplateID || d.Kind == "stage" && d.Stage == u.FirstStage {
					s.LogicalUnknown = sum(s.LogicalUnknown, "1")
				}
				if u.Active && (d.Kind == "overall" || d.Kind == "template" && d.TemplateID == u.TemplateID || d.Kind == "stage" && d.Stage == u.AttemptStage) {
					s.UnknownAttempts = sum(s.UnknownAttempts, "1")
				}
			}
		}
		if h.SchemaVersion == 2 && h.Checkpoint != nil && d.Kind != "stage" {
			s.CPU = cpuSummary(h.Checkpoint.CPUStats)
			setRates(&s, h.Checkpoint.ElapsedNS)
		}
		if d.Kind == "template" && h.SchemaVersion != 2 {
			s.CPU = cpuStats(h.Buckets, d.TemplateID)
		}
		ds := DimensionSummary{d.Kind, d.TemplateID, d.Stage, label, s}
		switch d.Kind {
		case "overall":
			if o.TemplateID == "" {
				r.Summary = s
			}
		case "template":
			r.Templates = append(r.Templates, ds)
			if d.TemplateID == o.TemplateID {
				r.Summary = s
				found = true
			}
		case "stage":
			r.Stages = append(r.Stages, ds)
		}
	}
	if !found {
		return r, fmt.Errorf("unknown template scope")
	}
	if h.Checkpoint != nil && o.TemplateID == "" {
		r.Summary.Desired = decimal(h.Checkpoint.Desired)
		r.Summary.SchedulerMisses = decimal(h.Checkpoint.SchedulerMisses)
		r.Summary.CapacityMisses = decimal(h.Checkpoint.CapacityMisses)
	}
	buckets := []engine.Bucket{}
	total := 0
	for _, b := range h.Buckets {
		if b.TemplateID == o.TemplateID {
			total++
			if b.Index >= o.FromIndex {
				buckets = append(buckets, b)
			}
		}
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Index < buckets[j].Index })
	r.Range = RangeInfo{FromIndex: strconv.FormatInt(o.FromIndex, 10), TotalBuckets: strconv.Itoa(total), TotalSamples: "0"}
	if h.Page == nil && o.Limit > 0 && len(buckets) > o.Limit {
		next := strconv.FormatInt(buckets[o.Limit].Index, 10)
		r.Range.NextIndex = &next
		buckets = buckets[:o.Limit]
	}
	for _, b := range buckets {
		r.Points = append(r.Points, point(b, h.Snapshot.Draft.Profile.Mode, h.Snapshot.Draft.Profile.RetryAccounting))
	}
	samples := []engine.Sample{}
	for _, s := range h.Samples {
		if o.TemplateID == "" || s.TemplateID == o.TemplateID {
			samples = append(samples, s)
		}
	}
	r.Range.TotalSamples = strconv.Itoa(len(samples))
	start := o.SampleOffset
	if h.Page != nil {
		start = 0
	}
	if start > len(samples) {
		start = len(samples)
	}
	end := len(samples)
	if o.SampleLimit > 0 && end-start > o.SampleLimit {
		end = start + o.SampleLimit
		r.Range.NextSampleOffset = &end
	}
	r.Samples = append(r.Samples, samples[start:end]...)
	r.Summary.CPU = cpuStats(h.Buckets, o.TemplateID)
	if h.SchemaVersion == 2 && h.Checkpoint != nil {
		r.Summary.CPU = cpuSummary(h.Checkpoint.CPUStats)
		setRates(&r.Summary, h.Checkpoint.ElapsedNS)
	}
	if h.Page == nil && o.Limit == 0 && o.FromIndex == 0 {
		r.ChartScopes = map[string][]ChartPoint{}
		for _, b := range h.Buckets {
			r.ChartScopes[b.TemplateID] = append(r.ChartScopes[b.TemplateID], point(b, h.Snapshot.Draft.Profile.Mode, h.Snapshot.Draft.Profile.RetryAccounting))
		}
	}
	if o.Population != "" && o.Population != "all" && o.Population != "all-finished" {
		for i := range r.Points {
			r.Points[i].P50MS = nil
			r.Points[i].P90MS = nil
			r.Points[i].P95MS = nil
			r.Points[i].P99MS = nil
		}
		for key, points := range r.ChartScopes {
			for i := range points {
				points[i].P50MS = nil
				points[i].P90MS = nil
				points[i].P95MS = nil
				points[i].P99MS = nil
			}
			r.ChartScopes[key] = points
		}
	}
	if h.SchemaVersion == 2 {
		if err := adoptEvidence(&r, h, o); err != nil {
			return r, err
		}
	}
	return r, nil
}

func point(b engine.Bucket, mode, accounting string) ChartPoint {
	l := latency(b.Durations)
	p := ChartPoint{Index: strconv.FormatInt(b.Index, 10), StartMS: float64(b.StartNS) / 1e6, EndMS: float64(b.EndNS) / 1e6, ErrorCount: decimal(b.Errors), P50MS: l.P50MS, P90MS: l.P90MS, P95MS: l.P95MS, P99MS: l.P99MS, CPU: b.CPU}
	seconds := float64(b.EndNS-b.StartNS) / 1e9
	if seconds <= 0 {
		return p
	}
	p.InitialRate = ptr(float64(b.Initial) / seconds)
	p.RetryRate = ptr(float64(b.Retries) / seconds)
	p.TotalRate = ptr(*p.InitialRate + *p.RetryRate)
	if mode == "open" && b.TemplateID == "" {
		p.DesiredRate = ptr(float64(b.Desired) / seconds)
		actual := *p.InitialRate
		if accounting == "within" {
			actual = *p.TotalRate
		}
		short := *p.DesiredRate - actual
		if short < 0 {
			short = 0
		}
		p.Shortfall = &short
	}
	return p
}

func cpuStats(buckets []engine.Bucket, scope string) CPUStats {
	c := CPUStats{Count: "0", Missing: "0"}
	n := 0
	sum := 0.0
	for _, b := range buckets {
		if b.TemplateID != scope || b.CPU == nil {
			continue
		}
		v := *b.CPU
		n++
		sum += v
		if c.MinPercent == nil || v < *c.MinPercent {
			c.MinPercent = ptr(v)
		}
		if c.MaxPercent == nil || v > *c.MaxPercent {
			c.MaxPercent = ptr(v)
		}
	}
	c.Count = strconv.Itoa(n)
	if n > 0 {
		c.MeanPercent = ptr(sum / float64(n))
	}
	return c
}
