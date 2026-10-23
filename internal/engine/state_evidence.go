package engine

import (
	"highloader/internal/metrics"
	"math"
	"time"
)

func (r *Runner) sampleCPU() {
	if r.options.CPU == nil || r.finalQueued {
		return
	}
	sample := r.options.CPU.Latest()
	if len(sample.ID) > 256 || len(sample.Reason) > 512 {
		r.fail("CPU sample metadata bound exceeded")
		return
	}
	r.cpuDiagnostic = ""
	if sample.ID == "" || sample.At.IsZero() {
		r.cpuDiagnostic = sample.Reason
		if r.cpuDiagnostic == "" {
			r.cpuDiagnostic = "CPU sample identity unavailable"
		}
		return
	}
	if sample.At.Before(r.origin) || sample.At.After(r.now) || sample.ID == r.lastCPU.ID || !r.lastCPU.At.IsZero() && !sample.At.After(r.lastCPU.At) {
		return
	}
	index := int64(sample.At.Sub(r.origin) / time.Second)
	late := index < r.buckets.Current().Index
	if _, exists := r.cpuAmendments[index]; late && !exists && len(r.cpuAmendments) >= 12 {
		r.fail("CPU amendment backpressure")
		return
	}
	valid := sample.Percent != nil && !math.IsNaN(*sample.Percent) && !math.IsInf(*sample.Percent, 0) && *sample.Percent >= 0 && *sample.Percent <= 100
	if !valid {
		sample.Percent = nil
		if sample.Reason == "" {
			sample.Reason = "invalid or missing CPU sample"
		}
		if r.cpuStats.Missing == math.MaxUint64 {
			r.fail("CPU counter overflow")
			return
		}
		r.cpuStats.Missing++
	} else {
		valueCopy := *sample.Percent
		sample.Percent = &valueCopy
		if r.cpuStats.Valid == math.MaxUint64 {
			r.fail("CPU counter overflow")
			return
		}
		value := *sample.Percent
		r.cpuStats.Valid++
		r.cpuStats.Sum += value
		if r.cpuStats.Min == nil || value < *r.cpuStats.Min {
			v := value
			r.cpuStats.Min = &v
		}
		if r.cpuStats.Max == nil || value > *r.cpuStats.Max {
			v := value
			r.cpuStats.Max = &v
		}
	}
	r.lastCPU = sample
	apply := func(meta Bucket) Bucket {
		meta.CPU, meta.CPUAt, meta.CPUIdentity, meta.CPUReason = sample.Percent, sample.At, sample.ID, sample.Reason
		return meta
	}
	// A scheduler jump may leave the acquisition interval ahead of Current.
	// Retain this one CPU-only key before the seal loop reaches it; never invent users.
	if meta, exists := r.baseMeta[index]; exists || index >= r.buckets.Current().Index {
		r.baseMeta[index] = apply(meta)
	}
	if meta, exists := r.liveMeta[index]; exists || index >= r.buckets.Current().Index {
		r.liveMeta[index] = apply(meta)
	}
	if index == r.buckets.Current().Index {
		r.buckets.Current().CPU = sample.Percent
	}
	if late {
		r.cpuAmendments[index] = CPUAmendment{Index: index, At: sample.At, Identity: sample.ID, Percent: sample.Percent, Reason: sample.Reason}
	}
}

func (r *Runner) captureBase() {
	if r.finalQueued {
		return
	}
	current := r.buckets.Current()
	meta := Bucket{SnapshotAt: r.now, CPUReason: "no CPU sample in interval"}
	if r.options.CPU == nil {
		meta.CPUReason = "CPU source unavailable"
	}
	if r.cpuDiagnostic != "" {
		meta.CPUReason = r.cpuDiagnostic
	}
	if r.snapshot.Draft.Profile.Mode == "closed" {
		target := int64(0)
		if r.state == "running" {
			target = r.snapshot.Draft.Profile.Steps[r.stage].Level
		}
		active, retiring := int64(0), int64(0)
		for _, u := range r.users {
			if u.retiring {
				if u.busy {
					retiring++
				}
			} else {
				active++
			}
		}
		meta.TargetUsers = &target
		meta.ActiveUsers = &active
		meta.RetiringUsers = &retiring
	}
	if !r.lastCPU.At.IsZero() {
		elapsed := r.lastCPU.At.Sub(r.origin)
		if elapsed >= current.Start && elapsed < current.Start+time.Second {
			meta.CPUIdentity = r.lastCPU.ID
			meta.CPUAt = r.lastCPU.At
			meta.CPUReason = r.lastCPU.Reason
			if r.lastCPU.Percent != nil {
				v := *r.lastCPU.Percent
				meta.CPU = &v
			}
		}
	}
	r.baseMeta[current.Index] = meta
	r.liveMeta[current.Index] = meta
	current.CPU = meta.CPU
}

func (r *Runner) baseBucket(b metrics.Bucket, template string) Bucket {
	out := Bucket{TemplateID: template, Index: b.Index, StartNS: int64(b.Start), EndNS: int64(b.End), Desired: b.Desired, Initial: b.Initial, Retries: b.Retries, Missed: b.Missed}
	if template == "" {
		meta := r.baseMeta[b.Index]
		out.TargetUsers = meta.TargetUsers
		out.ActiveUsers = meta.ActiveUsers
		out.RetiringUsers = meta.RetiringUsers
		out.SnapshotAt = meta.SnapshotAt
		out.CPU = meta.CPU
		out.CPUAt = meta.CPUAt
		out.CPUIdentity = meta.CPUIdentity
		out.CPUReason = meta.CPUReason
		if out.CPUReason == "" && out.CPU == nil {
			out.CPUReason = "no CPU sample in interval"
		}
	}
	return out
}

func (r *Runner) livePoints() []metrics.Point {
	points := r.buckets.Live()
	for i := range points {
		point := &points[i]
		if value := r.liveCompletion[point.Index]; value != nil {
			for i, n := range value.Attempts {
				point.Completions += uint64(n)
				if i < 4 {
					point.MeasuredCompletions += uint64(n)
				}
			}
			point.Canceled = uint64(value.Attempts[metrics.Canceled])
			point.Unknown = uint64(value.Attempts[metrics.Unknown])
			point.Errors = uint64(value.Attempts[metrics.HTTPError] + value.Attempts[metrics.TechnicalError] + value.Attempts[metrics.HTTPTechnicalError])
			d, err := value.AttemptDistribution(metrics.DefaultPopulation)
			if err != nil {
				continue
			}
			targets := []**time.Duration{&point.P50, &point.P90, &point.P95, &point.P99}
			for j, q := range []float64{.5, .9, .95, .99} {
				if v, ok := d.Quantile(q); ok {
					x := v
					*targets[j] = &x
				}
			}
		}
		if width := (point.End - point.Start).Seconds(); width > 0 {
			total := float64(point.Completions) / width
			measured := float64(point.MeasuredCompletions) / width
			point.CompletedRate = &total
			point.MeasuredCompletionRate = &measured
		}
		if meta, ok := r.liveMeta[point.Index]; ok {
			point.CPU = meta.CPU
			point.CPUReason = meta.CPUReason
			point.TargetUsers = meta.TargetUsers
			point.ActiveUsers = meta.ActiveUsers
			point.RetiringUsers = meta.RetiringUsers
		}
	}
	return points
}
