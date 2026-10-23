package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"
)

func Compare(left, right RunReport, mapping map[string]string) (Comparison, error) {
	c := Comparison{SchemaVersion: 1, Runs: [2]RunReport{left, right}, Alignment: "elapsed", Differences: []Difference{}, Deltas: []Delta{}}
	c.Deltas = deltas("summary", left.Summary, right.Summary)
	c.Deltas = append(c.Deltas, rateDeltas("summary", left, right, left.TemplateID, right.TemplateID)...)
	ld, rd := map[string]any{}, map[string]any{}
	a, _ := json.Marshal(left.Snapshot.Draft)
	b, _ := json.Marshal(right.Snapshot.Draft)
	ad := json.NewDecoder(bytes.NewReader(a))
	ad.UseNumber()
	if err := ad.Decode(&ld); err != nil {
		return c, err
	}
	bd := json.NewDecoder(bytes.NewReader(b))
	bd.UseNumber()
	if err := bd.Decode(&rd); err != nil {
		return c, err
	}
	keys := []string{}
	for k := range ld {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !reflect.DeepEqual(ld[k], rd[k]) {
			c.Differences = append(c.Differences, Difference{"draft." + k, ld[k], rd[k]})
		}
	}
	existsL, existsR := map[string]bool{}, map[string]bool{}
	for _, d := range left.Templates {
		existsL[d.TemplateID] = true
	}
	for _, d := range right.Templates {
		existsR[d.TemplateID] = true
	}
	used := map[string]bool{}
	for l, r := range mapping {
		if !existsL[l] || !existsR[r] || used[r] {
			return c, fmt.Errorf("invalid template mapping")
		}
		used[r] = true
	}
	used = map[string]bool{}
	for _, l := range left.Templates {
		target := mapping[l.TemplateID]
		if target == "" && existsR[l.TemplateID] {
			target = l.TemplateID
		}
		if target == "" {
			digest := ""
			for _, t := range left.Snapshot.Draft.Templates {
				if t.ID == l.TemplateID {
					digest = t.Digest
				}
			}
			matches := []string{}
			for _, t := range right.Snapshot.Draft.Templates {
				if digest != "" && t.Digest == digest && existsR[t.ID] {
					matches = append(matches, t.ID)
				}
			}
			if len(matches) == 1 {
				target = matches[0]
			}
		}
		if target == "" || used[target] {
			c.Differences = append(c.Differences, Difference{"template.unmatched", l.TemplateID, nil})
			continue
		}
		used[target] = true
		for _, r := range right.Templates {
			if r.TemplateID == target {
				c.Deltas = append(c.Deltas, deltas("template."+l.TemplateID, l.Summary, r.Summary)...)
				c.Deltas = append(c.Deltas, rateDeltas("template."+l.TemplateID, left, right, l.TemplateID, r.TemplateID)...)
			}
		}
	}
	for _, r := range right.Templates {
		if !used[r.TemplateID] {
			c.Differences = append(c.Differences, Difference{"template.unmatched", nil, r.TemplateID})
		}
	}
	return c, nil
}

func deltas(prefix string, left, right Summary) []Delta {
	a, b := map[string]any{}, map[string]any{}
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	json.Unmarshal(x, &a)
	json.Unmarshal(y, &b)
	keys := []string{}
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []Delta{}
	for _, k := range keys {
		ls, ok := a[k].(string)
		if !ok {
			continue
		}
		rs, ok := b[k].(string)
		if !ok {
			continue
		}
		l, lok := new(big.Int).SetString(ls, 10)
		r, rok := new(big.Int).SetString(rs, 10)
		if !lok || !rok {
			continue
		}
		diff := new(big.Int).Sub(r, l)
		d := Delta{Key: prefix + "." + k, Before: ls, After: rs, Absolute: diff.String()}
		if l.Sign() != 0 {
			v, _ := new(big.Rat).Mul(new(big.Rat).SetFrac(diff, l), big.NewRat(100, 1)).Float64()
			d.Percent = &v
		}
		out = append(out, d)
	}
	out = append(out, latencyDeltas(prefix+".attemptLatency", left.AttemptLatency, right.AttemptLatency)...)
	out = append(out, latencyDeltas(prefix+".logicalLatency", left.LogicalLatency, right.LogicalLatency)...)
	for i, key := range []string{"minPercent", "meanPercent", "maxPercent"} {
		a := []*float64{left.CPU.MinPercent, left.CPU.MeanPercent, left.CPU.MaxPercent}
		b := []*float64{right.CPU.MinPercent, right.CPU.MeanPercent, right.CPU.MaxPercent}
		out = append(out, numericDelta(prefix+".cpu."+key, a[i], b[i]))
	}
	return out
}

func latencyDeltas(prefix string, l, r Latency) []Delta {
	out := []Delta{}
	a := []*float64{l.MinMS, l.MeanMS, l.MaxMS, l.P50MS, l.P90MS, l.P95MS, l.P99MS}
	b := []*float64{r.MinMS, r.MeanMS, r.MaxMS, r.P50MS, r.P90MS, r.P95MS, r.P99MS}
	keys := []string{"minMs", "meanMs", "maxMs", "p50Ms", "p90Ms", "p95Ms", "p99Ms"}
	for i := range a {
		d := Delta{Key: prefix + "." + keys[i], Before: a[i], After: b[i]}
		if a[i] != nil && b[i] != nil {
			diff := *b[i] - *a[i]
			d.Absolute = diff
			if *a[i] != 0 {
				d.Percent = ptr(diff / *a[i] * 100)
			}
		}
		out = append(out, d)
	}
	return out
}

func numericDelta(key string, a, b *float64) Delta {
	d := Delta{Key: key, Before: a, After: b}
	if a != nil && b != nil {
		diff := *b - *a
		d.Absolute = diff
		if *a != 0 {
			d.Percent = ptr(diff / *a * 100)
		}
	}
	return d
}

func observedRates(r RunReport, scope string) [3]*float64 {
	var result [3]*float64
	points, ok := r.ChartScopes[scope]
	if !ok {
		if scope != r.TemplateID || r.Range.NextIndex != nil || r.Range.FromIndex != "0" || r.Range.TotalBuckets != fmt.Sprint(len(r.Points)) {
			return result
		}
		points = r.Points
	}
	points = append([]ChartPoint(nil), points...)
	sort.Slice(points, func(i, j int) bool { return points[i].StartMS < points[j].StartMS })
	seconds := 0.0
	totals := [3]float64{}
	end := 0.0
	for i, p := range points {
		if p.EndMS <= p.StartMS || i > 0 && p.StartMS < end || p.InitialRate == nil || p.RetryRate == nil || p.TotalRate == nil {
			return result
		}
		duration := (p.EndMS - p.StartMS) / 1000
		seconds += duration
		totals[0] += *p.InitialRate * duration
		totals[1] += *p.RetryRate * duration
		totals[2] += *p.TotalRate * duration
		end = p.EndMS
	}
	if seconds > 0 {
		for i := range result {
			result[i] = ptr(totals[i] / seconds)
		}
	}
	return result
}

func rateDeltas(prefix string, l, r RunReport, ls, rs string) []Delta {
	a, b := observedRates(l, ls), observedRates(r, rs)
	label := "observedIntervals"
	left, right := scopeSummary(l, ls), scopeSummary(r, rs)
	if left.MeasurementSeconds != nil && right.MeasurementSeconds != nil {
		a = [3]*float64{left.InitialRate, left.RetryRate, left.TotalRate}
		b = [3]*float64{right.InitialRate, right.RetryRate, right.TotalRate}
		label = "runElapsed"
	}
	out := []Delta{}
	for i, k := range []string{"initialPerSecond", "retryPerSecond", "totalPerSecond"} {
		out = append(out, numericDelta(prefix+"."+label+"."+k, a[i], b[i]))
	}
	return out
}
func scopeSummary(r RunReport, scope string) Summary {
	if scope == r.TemplateID {
		return r.Summary
	}
	for _, d := range r.Templates {
		if d.TemplateID == scope {
			return d.Summary
		}
	}
	return Summary{}
}
