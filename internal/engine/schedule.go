package engine

import (
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math/bits"
	"time"
)

type selector struct {
	mode    string
	weights []float64
	rng     Random
	cursor  int
	bag     []int
}

func (s *selector) next() int {
	n := len(s.weights)
	switch s.mode {
	case "uniform_random":
		return s.rng.Intn(n)
	case "weighted_random":
		// Normalize before summing so valid large weights cannot overflow.
		max := 0.0
		for _, w := range s.weights {
			if w > max {
				max = w
			}
		}
		total := 0.0
		for _, w := range s.weights {
			total += w / max
		}
		v := s.rng.Float64() * total
		for i, w := range s.weights {
			v -= w / max
			if v < 0 {
				return i
			}
		}
		return n - 1
	case "shuffled_round":
		if s.cursor == len(s.bag) {
			s.bag = make([]int, n)
			for i := range s.bag {
				s.bag[i] = i
			}
			for i := n - 1; i > 0; i-- {
				j := s.rng.Intn(i + 1)
				s.bag[i], s.bag[j] = s.bag[j], s.bag[i]
			}
			s.cursor = 0
		}
		i := s.bag[s.cursor]
		s.cursor++
		return i
	default:
		i := s.cursor % n
		s.cursor++
		return i
	}
}

func retryDelay(p domain.Policy, attempt uint64, rng Random) time.Duration {
	d := p.DelayMS
	cap := p.MaxDelayMS
	if p.DelayMode == "exponential" {
		for i := uint64(1); i < attempt && d < cap; i++ {
			if d > cap/2 {
				d = cap
			} else {
				d *= 2
			}
		}
	}
	if d > cap {
		d = cap
	}
	v := time.Duration(d) * time.Millisecond
	if p.Jitter && v > 0 {
		v = time.Duration(rng.Int63n(int64(v) + 1))
	}
	return v
}

func retryable(p domain.Policy, o metrics.Observation) bool {
	if o.Canceled || o.Unknown || o.DeadlineInterruption {
		return false
	}
	if (o.NetworkFailure || o.BodyReadFailure) && p.Network {
		return true
	}
	if o.AttemptTimeout && p.Timeouts {
		return true
	}
	if o.Status >= 200 && o.Status <= 299 {
		return false
	}
	for _, r := range p.Statuses {
		if o.Status >= r.From && o.Status <= r.To {
			return true
		}
	}
	return false
}

func arrivals(level int64, d time.Duration) uint64 {
	if d <= 0 || level <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(d%time.Second), uint64(level))
	part, _ := bits.Div64(hi, lo, uint64(time.Second))
	return uint64(d/time.Second)*uint64(level) + part
}

func quantumDelay(origin, now time.Time) time.Duration {
	// Adding a full quantum after a late wake shifts every later wake.
	// Remainder subtraction avoids overflow from rounding elapsed upward.
	elapsed := now.Sub(origin)
	if elapsed < 0 {
		return time.Millisecond
	}
	return time.Millisecond - elapsed%time.Millisecond
}

func nextBudgetDelay(elapsed time.Duration, level int64) time.Duration {
	// Whole seconds contribute exactly level permits; only the remainder matters.
	// All products stay below one million even at the last quantum of a second.
	within := elapsed % time.Second
	processedMS := int64(within / time.Millisecond)
	count := (processedMS + 1) * level / 1000
	endMS := ((count+1)*1000 + level - 1) / level
	return time.Duration(endMS-1)*time.Millisecond - within
}

func (r *Runner) wakeDelay(now, lastPublish time.Time) time.Duration {
	fallback := quantumDelay(r.origin, now)
	if r.state != "running" || r.snapshot.Draft.Profile.Mode != "open" || len(r.records) != 0 || r.workers != 0 || len(r.canceled) != 0 {
		return fallback
	}
	step := r.snapshot.Draft.Profile.Steps[r.stage]
	if step.Level < 1 || step.Level >= 1000 {
		return fallback
	}
	// Rearm only after schedule has processed the current quantum;
	// the next permit lives at its quantum START (999ms for 1RPS), not its end.
	deadline := r.origin.Add(r.elapsed).Add(nextBudgetDelay(r.elapsed-r.stageStart, step.Level))
	for _, cap := range []time.Time{
		lastPublish.Add(100 * time.Millisecond),
		r.origin.Add(r.buckets.Current().Start).Add(time.Second),
		r.origin.Add(r.stageStart).Add(time.Duration(step.DurationSeconds) * time.Second),
		r.origin.Add(r.total),
	} {
		if cap.Before(deadline) {
			deadline = cap
		}
	}
	if !deadline.After(now) {
		// Processing crossed the chosen boundary. Re-enter on the next aligned
		// quantum; advance accounts skipped demand, never a zero-delay catch-up.
		return fallback
	}
	return deadline.Sub(now)
}
