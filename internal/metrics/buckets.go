package metrics

import (
	"errors"
	"time"
)

var ErrBackpressure = errors.New("unacknowledged bucket limit reached")

type Bucket struct {
	Index                                     int64
	Start, End                                time.Duration
	Desired, Initial, Retries, Missed, Errors uint64
	Durations                                 Distribution
	CPU                                       *float64
}
type Point struct {
	CompletedRate                             *float64
	MeasuredCompletionRate                    *float64
	Completions                               uint64 `json:"Completions,string"`
	MeasuredCompletions                       uint64 `json:"MeasuredCompletions,string"`
	Canceled                                  uint64 `json:"Canceled,string"`
	Unknown                                   uint64 `json:"Unknown,string"`
	TargetUsers                               *int64 `json:"TargetUsers,omitempty,string"`
	ActiveUsers                               *int64 `json:"ActiveUsers,omitempty,string"`
	RetiringUsers                             *int64 `json:"RetiringUsers,omitempty,string"`
	CPUReason                                 string
	Index                                     int64
	Start, End                                time.Duration
	Desired, Initial, Retries, Missed, Errors uint64
	TotalRate                                 *float64
	P50, P90, P95, P99                        *time.Duration
	CPU                                       *float64
}
type Buckets struct {
	current               Bucket
	pending               []Bucket
	live                  []Point
	maxPending, liveLimit int
	acked                 int64
}

func NewBuckets(maxPending, liveLimit int) (*Buckets, error) {
	if maxPending < 1 || liveLimit < 1 {
		return nil, ErrInvalid
	}
	return &Buckets{maxPending: maxPending, liveLimit: liveLimit, acked: -1}, nil
}

func (b *Buckets) Current() *Bucket { return &b.current }

func (b *Buckets) Seal(end time.Duration) error {
	if end < b.current.Start || end-b.current.Start > time.Second {
		return ErrInvalid
	}
	if len(b.pending) >= b.maxPending {
		return ErrBackpressure
	}
	v := b.current
	v.End = end
	b.pending = append(b.pending, v)
	p := Point{Index: v.Index, Start: v.Start, End: end, Desired: v.Desired, Initial: v.Initial, Retries: v.Retries, Missed: v.Missed, Errors: v.Errors}
	if v.CPU != nil {
		x := *v.CPU
		p.CPU = &x
	}
	if end > v.Start {
		r := (float64(v.Initial) + float64(v.Retries)) / (end - v.Start).Seconds()
		p.TotalRate = &r
	}
	targets := []**time.Duration{&p.P50, &p.P90, &p.P95, &p.P99}
	for i, q := range []float64{.5, .9, .95, .99} {
		if value, ok := v.Durations.Quantile(q); ok {
			x := value
			*targets[i] = &x
		}
	}
	b.live = append(b.live, p)
	if len(b.live) > b.liveLimit {
		copy(b.live, b.live[len(b.live)-b.liveLimit:])
		b.live = b.live[:b.liveLimit]
	}
	b.current = Bucket{Index: v.Index + 1, Start: end}
	return nil
}

func (b *Buckets) Checkpoint() []Bucket {
	out := make([]Bucket, len(b.pending))
	for i, v := range b.pending {
		out[i] = v
		out[i].Durations = Distribution{}
		_ = out[i].Durations.Merge(&v.Durations)
		if v.CPU != nil {
			x := *v.CPU
			out[i].CPU = &x
		}
	}
	return out
}

func (b *Buckets) Ack(through int64) error {
	if through <= b.acked {
		return nil
	}
	if through >= b.current.Index {
		return ErrInvalid
	}
	n := 0
	for n < len(b.pending) && b.pending[n].Index <= through {
		b.pending[n] = Bucket{}
		n++
	}
	b.pending = b.pending[n:]
	b.acked = through
	return nil
}

func (b *Buckets) Live() []Point {
	out := append([]Point(nil), b.live...)
	for i := range out {
		p := &out[i]
		if p.TotalRate != nil {
			x := *p.TotalRate
			p.TotalRate = &x
		}
		if p.CPU != nil {
			x := *p.CPU
			p.CPU = &x
		}
		for _, target := range []**time.Duration{&p.P50, &p.P90, &p.P95, &p.P99} {
			if *target != nil {
				x := **target
				*target = &x
			}
		}
	}
	return out
}
