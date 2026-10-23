package report

import (
	"encoding/json"
	"fmt"
	"highloader/internal/engine"
	"highloader/internal/history"
	"sort"
)

func Overlay(h history.Report, c engine.Checkpoint) history.Report {
	if c.RunID != h.RunID || h.Recovery != nil || h.Checkpoint != nil && (c.Sequence <= h.Checkpoint.Sequence || c.Cut.Before(h.Checkpoint.Cut)) {
		return h
	}
	raw, e := json.Marshal(h)
	if e != nil {
		return h
	}
	var out history.Report
	if json.Unmarshal(raw, &out) != nil {
		return h
	}
	raw, e = json.Marshal(c)
	if e != nil {
		return h
	}
	var cp engine.Checkpoint
	if json.Unmarshal(raw, &cp) != nil {
		return h
	}
	out.Checkpoint = &cp
	if out.Page != nil {
		cp.Buckets = filterPageBuckets(cp.Buckets, &out)
		cp.Samples = filterPageSamples(cp.Samples, out)
	}
	out.State = cp.State
	out.Reason = cp.Reason
	dimensions := map[string]int{}
	for i, d := range out.Aggregates {
		dimensions[fmt.Sprintf("%s/%s/%d", d.Kind, d.TemplateID, d.Stage)] = i
	}
	for _, d := range cp.Aggregates {
		k := fmt.Sprintf("%s/%s/%d", d.Kind, d.TemplateID, d.Stage)
		if i, ok := dimensions[k]; ok {
			out.Aggregates[i] = d
		} else {
			dimensions[k] = len(out.Aggregates)
			out.Aggregates = append(out.Aggregates, d)
		}
	}
	buckets := map[string]int{}
	for i, b := range out.Buckets {
		buckets[fmt.Sprintf("%s/%d", b.TemplateID, b.Index)] = i
	}
	for _, b := range cp.Buckets {
		k := fmt.Sprintf("%s/%d", b.TemplateID, b.Index)
		for i, a := range out.BucketAvailability {
			if a.TemplateID == b.TemplateID && a.Index == b.Index {
				out.BucketAvailability[i].BasePresent = true
				out.BucketAvailability[i].IntervalKnown = b.EndNS > b.StartNS
			}
		}
		if i, ok := buckets[k]; ok {
			// a newer base revision can carry an older CPU observation.
			old := out.Buckets[i]
			if old.CPUAt.After(b.CPUAt) {
				b.CPU, b.CPUAt, b.CPUIdentity, b.CPUReason = old.CPU, old.CPUAt, old.CPUIdentity, old.CPUReason
			}
			out.Buckets[i] = b
		} else {
			buckets[k] = len(out.Buckets)
			out.Buckets = append(out.Buckets, b)
		}
	}
	for _, amendment := range cp.CPUAmendments {
		i, ok := buckets[fmt.Sprintf("/%d", amendment.Index)]
		if !ok {
			continue
		}
		basePresent := true
		for _, availability := range out.BucketAvailability {
			if availability.TemplateID == "" && availability.Index == amendment.Index {
				basePresent = availability.BasePresent
				break
			}
		}
		base := &out.Buckets[i]
		if basePresent && amendment.At.After(base.CPUAt) {
			base.CPU, base.CPUAt, base.CPUIdentity, base.CPUReason = amendment.Percent, amendment.At, amendment.Identity, amendment.Reason
		}
	}
	samples := map[string]int{}
	for i, s := range out.Samples {
		samples[fmt.Sprintf("%d/%d", s.Sequence, s.Attempt)] = i
	}
	for _, s := range cp.Samples {
		k := fmt.Sprintf("%d/%d", s.Sequence, s.Attempt)
		if i, ok := samples[k]; ok {
			out.Samples[i] = s
		} else {
			samples[k] = len(out.Samples)
			out.Samples = append(out.Samples, s)
		}
	}
	if err := mergeOverlayCompletions(&out, cp, h.CompletionSequence); err != nil {
		return h
	}
	return out
}

func mergeOverlayCompletions(h *history.Report, cp engine.Checkpoint, cursor uint64) error {
	values := map[string]int{}
	for i, d := range h.Completions {
		values[bucketKey(d.TemplateID, d.Index)] = i
	}
	keys := map[string]int{}
	for i, b := range h.Buckets {
		keys[bucketKey(b.TemplateID, b.Index)] = i
	}
	deltas := []engine.CompletionDelta{}
	for _, batch := range cp.CompletionBatches {
		if batch.Sequence <= cursor {
			continue
		}
		if batch.Version != 1 || batch.Sequence != cursor+1 {
			return fmt.Errorf("noncontiguous overlay completion")
		}
		cursor = batch.Sequence
		deltas = append(deltas, batch.Deltas...)
	}
	deltas = append(deltas, cp.UnflushedCompletion...)
	for _, d := range deltas {
		key := bucketKey(d.TemplateID, d.Index)
		if h.Page != nil {
			if _, ok := keys[key]; !ok {
				continue
			}
		}
		if i, ok := values[key]; ok {
			if e := h.Completions[i].Value.Merge(d.Value); e != nil {
				return e
			}
		} else {
			if e := d.Value.Validate(); e != nil {
				return e
			}
			values[key] = len(h.Completions)
			h.Completions = append(h.Completions, d)
		}
		if _, ok := keys[key]; !ok {
			b := engine.Bucket{TemplateID: d.TemplateID, Index: d.Index, StartNS: d.Index * 1e9, EndNS: d.Index * 1e9, CPUReason: "base interval unavailable"}
			if cp.ElapsedNS > b.StartNS {
				b.EndNS = b.StartNS + 1e9
				if b.EndNS > cp.ElapsedNS {
					b.EndNS = cp.ElapsedNS
				}
			}
			keys[key] = len(h.Buckets)
			h.Buckets = append(h.Buckets, b)
			h.BucketAvailability = append(h.BucketAvailability, history.BucketAvailability{TemplateID: d.TemplateID, Index: d.Index, IntervalKnown: b.EndNS > b.StartNS})
		}
	}
	return nil
}

func filterPageBuckets(buckets []engine.Bucket, h *history.Report) []engine.Bucket {
	keys := map[string]bool{}
	for _, b := range h.Buckets {
		keys[bucketKey(b.TemplateID, b.Index)] = true
	}
	out := []engine.Bucket{}
	additions := []engine.Bucket{}
	for _, b := range buckets {
		if keys[bucketKey(b.TemplateID, b.Index)] {
			out = append(out, b)
		} else if h.Page != nil && h.Page.NextIndex == nil && b.TemplateID == h.Page.Scope && b.Index >= h.Page.FromIndex {
			additions = append(additions, b)
		}
	}
	sort.Slice(additions, func(i, j int) bool { return additions[i].Index < additions[j].Index })
	room := h.Page.Limit - len(h.Buckets)
	if room > len(additions) {
		room = len(additions)
	}
	if room > 0 {
		out = append(out, additions[:room]...)
		h.VolatileAdditionalKeys += room
		for _, b := range additions[:room] {
			h.BucketAvailability = append(h.BucketAvailability, history.BucketAvailability{TemplateID: b.TemplateID, Index: b.Index, BasePresent: true, IntervalKnown: b.EndNS > b.StartNS})
		}
	}
	return out
}

func filterPageSamples(samples []engine.Sample, h history.Report) []engine.Sample {
	keys := map[string]bool{}
	for _, s := range h.Samples {
		keys[fmt.Sprintf("%d/%d", s.Sequence, s.Attempt)] = true
	}
	out := []engine.Sample{}
	for _, s := range samples {
		if keys[fmt.Sprintf("%d/%d", s.Sequence, s.Attempt)] {
			out = append(out, s)
		}
	}
	return out
}
