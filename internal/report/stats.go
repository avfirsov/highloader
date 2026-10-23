package report

import (
	"fmt"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"math/big"
	"strconv"
)

func decimal(v uint64) string { return strconv.FormatUint(v, 10) }
func sum(values ...string) string {
	n := new(big.Int)
	for _, v := range values {
		x, ok := new(big.Int).SetString(v, 10)
		if ok {
			n.Add(n, x)
		}
	}
	return n.String()
}
func at(values []string, i int) string {
	if i < len(values) && values[i] != "" {
		return values[i]
	}
	return "0"
}
func ptr(v float64) *float64 { return &v }

func latency(d metrics.Distribution) Latency {
	s := d.Summary()
	l := Latency{Count: decimal(s.Count), ResolutionLimited: decimal(s.ResolutionLimited)}
	if s.Min != nil {
		l.MinMS = ptr(float64(*s.Min) / 1e6)
		l.MaxMS = ptr(float64(*s.Max) / 1e6)
		l.MeanMS = ptr(*s.MeanNS / 1e6)
	}
	targets := []**float64{&l.P50MS, &l.P90MS, &l.P95MS, &l.P99MS}
	for i, q := range []float64{.5, .9, .95, .99} {
		if v, ok := d.Quantile(q); ok {
			*targets[i] = ptr(float64(v) / 1e6)
		}
	}
	return l
}

func summarize(a engine.Aggregate, pop string) (Summary, error) {
	s := Summary{CPU: CPUStats{Count: "0", Missing: "0"}, Initial: decimal(a.Initial), Retries: decimal(a.Retries), Attempts: sum(decimal(a.Initial), decimal(a.Retries)), LogicalStarted: decimal(a.Started), LogicalSuccess: at(a.Logical, 0), LogicalFailure: at(a.Logical, 1), LogicalDeadline: at(a.Logical, 2), LogicalCanceled: at(a.Logical, 3), LogicalUnknown: at(a.Logical, 4), HTTPErrors: "0", TechnicalErrors: decimal(a.TechnicalObserved), FailedAttempts: sum(at(a.Attempts, 1), at(a.Attempts, 2), at(a.Attempts, 3)), CanceledAttempts: at(a.Attempts, 4), UnknownAttempts: at(a.Attempts, 5), SchedulerMisses: "0", CapacityMisses: "0", Desired: "0", Statuses: map[string]string{}}
	for k, v := range a.Statuses {
		s.Statuses[k] = v
		n, e := strconv.Atoi(k)
		if e != nil {
			return s, fmt.Errorf("invalid status %q", k)
		}
		if n < 200 || n > 299 {
			s.HTTPErrors = sum(s.HTTPErrors, v)
		}
	}
	indices := []int{0, 1, 2, 3}
	logical := []int{0, 1, 2}
	switch pop {
	case "", "all", "all-finished":
	case "success":
		indices = []int{0}
		logical = []int{0}
	case "http":
		indices = []int{1, 3}
		logical = nil
	case "technical":
		indices = []int{2, 3}
		logical = nil
	default:
		return s, fmt.Errorf("unknown population %q", pop)
	}
	var ad, ld metrics.Distribution
	for _, i := range indices {
		if i < len(a.AttemptDurations) {
			if e := ad.Merge(&a.AttemptDurations[i]); e != nil {
				return s, e
			}
		}
	}
	for _, i := range logical {
		if i < len(a.LogicalDurations) {
			if e := ld.Merge(&a.LogicalDurations[i]); e != nil {
				return s, e
			}
		}
	}
	s.AttemptLatency = latency(ad)
	s.LogicalLatency = latency(ld)
	return s, nil
}
