package engine

import (
	"encoding/json"

	"highloader/internal/metrics"
	"math"
	"sort"
	"strconv"
	"time"
)

func (c Counter) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(uint64(c), 10))
}
func (c *Counter) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err == nil {
		*c = Counter(v)
	}
	return err
}

func (v CompletionValue) Validate() error {
	var attempts, logical uint64
	for i, n := range v.Attempts {
		if math.MaxUint64-attempts < uint64(n) {
			return metrics.ErrOverflow
		}
		attempts += uint64(n)
		if i < 4 && v.AttemptDurations[i].Summary().Count != uint64(n) {
			return metrics.ErrInvalid
		}
	}
	for i, n := range v.Logical {
		if math.MaxUint64-logical < uint64(n) {
			return metrics.ErrOverflow
		}
		logical += uint64(n)
		if i < 3 && v.LogicalDurations[i].Summary().Count != uint64(n) {
			return metrics.ErrInvalid
		}
	}
	return nil
}

func (v *CompletionValue) Merge(other CompletionValue) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if err := other.Validate(); err != nil {
		return err
	}
	out := clone(*v)
	for i, n := range other.Attempts {
		if uint64(n) > math.MaxUint64-uint64(out.Attempts[i]) {
			return metrics.ErrOverflow
		}
		out.Attempts[i] += n
	}
	for i, n := range other.Logical {
		if uint64(n) > math.MaxUint64-uint64(out.Logical[i]) {
			return metrics.ErrOverflow
		}
		out.Logical[i] += n
	}
	for i := range out.AttemptDurations {
		if err := out.AttemptDurations[i].Merge(&other.AttemptDurations[i]); err != nil {
			return err
		}
	}
	for i := range out.LogicalDurations {
		if err := out.LogicalDurations[i].Merge(&other.LogicalDurations[i]); err != nil {
			return err
		}
	}
	if err := out.Validate(); err != nil {
		return err
	}
	*v = out
	return nil
}

func (v CompletionValue) AttemptDistribution(population metrics.Population) (metrics.Distribution, error) {
	var out metrics.Distribution
	if population & ^metrics.Population(63) != 0 {
		return out, metrics.ErrInvalid
	}
	for i := range v.AttemptDurations {
		if population&(1<<i) != 0 {
			if err := out.Merge(&v.AttemptDurations[i]); err != nil {
				return metrics.Distribution{}, err
			}
		}
	}
	return out, nil
}

type completionKey struct {
	template string
	index    int64
}

func (r *Runner) recordCompletion(template string, at time.Time, attempt *metrics.Outcome, logical *metrics.LogicalOutcome, duration time.Duration) {
	index := int64(at.Sub(r.origin) / time.Second)
	if index < 0 {
		r.fail("completion precedes run origin")
		return
	}
	for _, scope := range []string{"", template} {
		key := completionKey{scope, index}
		value := r.completion[key]
		if value == nil {
			value = &CompletionValue{}
			r.completion[key] = value
		}
		if err := value.addEvent(attempt, logical, duration); err != nil {
			r.fail("completion overflow: " + err.Error())
			return
		}
	}
	if index >= int64(r.elapsed/time.Second)-599 {
		value := r.liveCompletion[index]
		if value == nil {
			value = &CompletionValue{}
			r.liveCompletion[index] = value
		}
		if err := value.addEvent(attempt, logical, duration); err != nil {
			r.fail(err.Error())
		}
	}
	// Reserve two keys per remaining logical/worker for forced terminal evidence.
	if len(r.completion) > r.completionLimit && !r.completionPressure {
		r.completionPressure = true

	}
}

func (r *Runner) flushCompletion(final bool) {
	if len(r.completion) == 0 {
		return
	}
	if len(r.completionBatches) >= 10 && !final {
		if !r.completionPressure {
			r.completionPressure = true
			r.fail("completion batch backpressure")
		}
		return
	}
	if len(r.completionBatches) >= 11 {
		return
	}
	r.completionSeq++
	batch := CompletionBatch{Version: 1, Sequence: r.completionSeq}
	for key, value := range r.completion {
		batch.Deltas = append(batch.Deltas, CompletionDelta{TemplateID: key.template, Index: key.index, Value: *value})
	}
	sort.Slice(batch.Deltas, func(i, j int) bool {
		a, b := batch.Deltas[i], batch.Deltas[j]
		if a.TemplateID == b.TemplateID {
			return a.Index < b.Index
		}
		return a.TemplateID < b.TemplateID
	})
	r.completionBatches = append(r.completionBatches, batch)
	r.completion = map[completionKey]*CompletionValue{}
}

func (r *Runner) completionSnapshot() ([]CompletionBatch, uint64) {
	return append([]CompletionBatch(nil), r.completionBatches...), r.completionSeq
}
func (r *Runner) unflushedCompletion() []CompletionDelta {
	var out []CompletionDelta
	for key, value := range r.completion {
		out = append(out, CompletionDelta{TemplateID: key.template, Index: key.index, Value: clone(*value)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TemplateID == out[j].TemplateID {
			return out[i].Index < out[j].Index
		}
		return out[i].TemplateID < out[j].TemplateID
	})
	return out
}

func (v *CompletionValue) addEvent(attempt *metrics.Outcome, logical *metrics.LogicalOutcome, duration time.Duration) error {
	if attempt != nil {
		if *attempt > metrics.Unknown {
			return metrics.ErrInvalid
		}
		if v.Attempts[*attempt] == Counter(math.MaxUint64) {
			return metrics.ErrOverflow
		}
		if *attempt < metrics.Canceled {
			if err := v.AttemptDurations[*attempt].Add(duration); err != nil {
				return err
			}
		}
		v.Attempts[*attempt]++
	}
	if logical != nil {
		if *logical > metrics.LogicalUnknown {
			return metrics.ErrInvalid
		}
		if v.Logical[*logical] == Counter(math.MaxUint64) {
			return metrics.ErrOverflow
		}
		if *logical < metrics.LogicalCanceled {
			if err := v.LogicalDurations[*logical].Add(duration); err != nil {
				return err
			}
		}
		v.Logical[*logical]++
	}
	return nil
}
