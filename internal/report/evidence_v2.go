package report

import (
	"fmt"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/metrics"
	"strconv"
)

func adoptEvidence(r *RunReport, h history.Report, o Options) error {
	values := map[string]engine.CompletionValue{}
	available := map[string]history.BucketAvailability{}
	for _, d := range h.Completions {
		values[bucketKey(d.TemplateID, d.Index)] = d.Value
	}
	for _, a := range h.BucketAvailability {
		available[bucketKey(a.TemplateID, a.Index)] = a
	}
	points := map[string]ChartPoint{}
	for _, b := range h.Buckets {
		key := bucketKey(b.TemplateID, b.Index)
		a, ok := available[key]
		if !ok {
			a = history.BucketAvailability{BasePresent: true, IntervalKnown: b.EndNS > b.StartNS}
		}
		p, e := evidencePoint(b, values[key], a, o.Population, h.Snapshot.Draft.Profile.Mode, h.Snapshot.Draft.Profile.RetryAccounting)
		if e != nil {
			return e
		}
		points[key] = p
	}
	for i, p := range r.Points {
		index, _ := strconv.ParseInt(p.Index, 10, 64)
		r.Points[i] = points[bucketKey(o.TemplateID, index)]
	}
	for scope, ps := range r.ChartScopes {
		for i, p := range ps {
			index, _ := strconv.ParseInt(p.Index, 10, 64)
			ps[i] = points[bucketKey(scope, index)]
		}
		r.ChartScopes[scope] = ps
	}
	if h.Page != nil {
		p := h.Page
		r.Range.Selection = "durable keys"
		if h.Checkpoint != nil && h.Checkpoint.Sequence > p.Sequence {
			r.Range.Selection = "durable keys with newer available values and bounded unsaved tail"
		}
		r.Range.VolatileAdditionalKeys = strconv.Itoa(h.VolatileAdditionalKeys)
		r.Range.FromIndex = strconv.FormatInt(p.FromIndex, 10)
		r.Range.TotalBuckets = strconv.FormatInt(p.TotalBuckets, 10)
		r.Range.TotalSamples = strconv.FormatInt(p.TotalSamples, 10)
		r.Range.NextSampleOffset = p.NextSampleOffset
		r.Range.NextIndex = nil
		if p.NextIndex != nil {
			next := strconv.FormatInt(*p.NextIndex, 10)
			r.Range.NextIndex = &next
		}
	}
	return nil
}

func bucketKey(scope string, index int64) string { return fmt.Sprintf("%s/%d", scope, index) }

func evidencePoint(b engine.Bucket, v engine.CompletionValue, a history.BucketAvailability, pop, mode, accounting string) (ChartPoint, error) {
	p := point(b, mode, accounting)
	if e := v.Validate(); e != nil {
		return p, e
	}
	mask := metrics.DefaultPopulation
	switch pop {
	case "success":
		mask = 1
	case "http":
		mask = metrics.HTTPPopulation
	case "technical":
		mask = metrics.TechnicalPopulation
	}
	d, e := v.AttemptDistribution(mask)
	if e != nil {
		return p, e
	}
	l := latency(d)
	p.P50MS = l.P50MS
	p.P90MS = l.P90MS
	p.P95MS = l.P95MS
	p.P99MS = l.P99MS
	p.BasePresent = a.BasePresent
	p.IntervalKnown = a.IntervalKnown
	p.CPUReason = b.CPUReason
	p.ErrorCount = sum(decimal(uint64(v.Attempts[1])), decimal(uint64(v.Attempts[2])), decimal(uint64(v.Attempts[3])))
	p.MeasuredFinished = sum(decimal(uint64(v.Attempts[0])), p.ErrorCount)
	p.CanceledCount = decimal(uint64(v.Attempts[4]))
	p.UnknownCount = decimal(uint64(v.Attempts[5]))
	p.CompletedCount = sum(p.MeasuredFinished, p.CanceledCount, p.UnknownCount)
	if a.IntervalKnown && b.EndNS > b.StartNS {
		n, _ := strconv.ParseFloat(p.CompletedCount, 64)
		p.CompletedRate = ptr(n / (float64(b.EndNS-b.StartNS) / 1e9))
	}
	if !a.BasePresent || !a.IntervalKnown {
		p.InitialRate = nil
		p.RetryRate = nil
		p.TotalRate = nil
		p.DesiredRate = nil
		p.Shortfall = nil
	}
	if !a.BasePresent {
		p.CPU = nil
	}
	if a.BasePresent && mode == "closed" {
		p.TargetUsers = intString(b.TargetUsers)
		p.ActiveUsers = intString(b.ActiveUsers)
		p.RetiringUsers = intString(b.RetiringUsers)
	}
	return p, nil
}

func intString(v *int64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatInt(*v, 10)
	return &s
}

func cpuSummary(s engine.CPUSummary) CPUStats {
	c := CPUStats{Count: decimal(s.Valid), Missing: decimal(s.Missing), MinPercent: s.Min, MaxPercent: s.Max}
	if s.Valid > 0 {
		c.MeanPercent = ptr(s.Sum / float64(s.Valid))
	}
	return c
}

func setRates(s *Summary, elapsed int64) {
	if elapsed <= 0 {
		return
	}
	seconds := float64(elapsed) / 1e9
	s.MeasurementSeconds = &seconds
	initial, e := strconv.ParseFloat(s.Initial, 64)
	if e != nil {
		return
	}
	retry, e := strconv.ParseFloat(s.Retries, 64)
	if e != nil {
		return
	}
	s.InitialRate = ptr(initial / seconds)
	s.RetryRate = ptr(retry / seconds)
	s.TotalRate = ptr((initial + retry) / seconds)
}
