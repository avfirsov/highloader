package engine

import (
	"context"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math"
	"testing"
	"time"
)

type phaseArm struct {
	delay time.Duration
	wake  chan time.Time
}
type phaseClock struct {
	testClock
	arms  chan phaseArm
	onArm func(phaseArm)
}

func (c *phaseClock) After(d time.Duration) <-chan time.Time {
	a := phaseArm{delay: d, wake: make(chan time.Time, 1)}
	c.arms <- a
	if c.onArm != nil {
		c.onArm(a)
	}
	return a.wake
}
func awaitArm(t *testing.T, c *phaseClock) phaseArm {
	t.Helper()
	select {
	case a := <-c.arms:
		return a
	case <-time.After(time.Second):
		t.Fatal("coordinator did not arm wake")
		return phaseArm{}
	}
}

func TestWakePhase(t *testing.T) {
	c := &phaseClock{arms: make(chan phaseArm, 8)}
	c.set(0)
	s := fixture()
	s.Draft.Profile.Steps[0].Level = 0
	r, err := New(s, Options{Clock: c, Transport: transportFunc(func(context.Context, Attempt) Result {
		panic("zero-rate stage dispatched")
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("coordinator did not release on shutdown")
		}
	})
	a := awaitArm(t, c)
	if a.delay != time.Millisecond {
		t.Fatal(a.delay)
	}
	for _, tc := range []struct{ elapsed, want time.Duration }{
		{1350 * time.Microsecond, 650 * time.Microsecond},
		{3200 * time.Microsecond, 800 * time.Microsecond},
		{4 * time.Millisecond, time.Millisecond},
	} {
		c.set(tc.elapsed)
		a.wake <- c.Now()
		a = awaitArm(t, c)
		if a.delay != tc.want {
			t.Fatalf("elapsed %s: rearm %s, want %s", tc.elapsed, a.delay, tc.want)
		}
	}
	t.Log("delayed and exact-boundary consumption retains origin phase without zero-delay spin")
}

func TestQuantumDelayBounds(t *testing.T) {
	origin := time.Unix(1000, 0)
	for _, d := range []time.Duration{-1, 0, 1, time.Millisecond - 1, time.Millisecond, math.MaxInt64} {
		got := quantumDelay(origin, origin.Add(d))
		if got <= 0 || got > time.Millisecond {
			t.Fatalf("%s: %s", d, got)
		}
		if d >= 0 && got != time.Millisecond-d%time.Millisecond {
			t.Fatal("lost phase", d, got)
		}
	}
}

func TestLateWakeStageAccounting(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 1000}, {DurationSeconds: 1, Level: 0}, {DurationSeconds: 1, Level: 2000}}
	r, c, _ := controlled(t, s)
	r.schedule()
	c.set(3200 * time.Microsecond)
	r.advance(c.Now())
	r.schedule()
	if r.desired != 4 || r.overall.Initial != 2 || r.schedulerMisses != 2 {
		t.Fatal(r.desired, r.overall.Initial, r.schedulerMisses)
	}
	c.set(1000350 * time.Microsecond)
	r.advance(c.Now())
	r.schedule()
	if r.stage != 1 || r.desired != 1000 || r.overall.Initial != 2 {
		t.Fatal("zero stage invented demand")
	}
	c.set(2002200 * time.Microsecond)
	r.advance(c.Now())
	r.schedule()
	if r.stage != 2 || r.overall.Initial != 4 || r.desired != 1006 || r.schedulerMisses != 1002 || r.capacityMisses != 0 {
		t.Fatal("late stage replay", r.desired, r.overall.Initial, r.schedulerMisses)
	}
	t.Log("skipped demand remains misses; zero stage silent; current quantum alone admits two permits")
}

func retryReviewSnapshot(delays, deadlines [3]int64) domain.RunSnapshot {
	s := fixture()
	s.Draft.Capacity = 3
	base, effective := s.Draft.Templates[0], s.Effective[0]
	s.Draft.Templates = nil
	s.Draft.SelectedIDs = nil
	s.Effective = nil
	for i, id := range []string{"a", "b", "c"} {
		tpl, req := base, effective
		tpl.ID = id
		req.TemplateID = id
		req.Policy.MaxRetries = 1
		req.Policy.DelayMS = delays[i]
		req.Policy.MaxDelayMS = 2000
		req.Policy.DeadlineMS = deadlines[i]
		s.Draft.Templates = append(s.Draft.Templates, tpl)
		s.Draft.SelectedIDs = append(s.Draft.SelectedIDs, id)
		s.Effective = append(s.Effective, req)
	}
	return s
}

func TestRetryDueSequence(t *testing.T) {
	s := retryReviewSnapshot([3]int64{30, 10, 10}, [3]int64{500, 500, 500})
	r, c, calls := controlled(t, s)
	for i := 0; i < 3; i++ {
		c.set(time.Duration(i) * time.Millisecond)
		r.advance(c.Now())
		r.schedule()
		<-calls
	}
	// Reverse completion order so heap insertion order cannot fake sequence order.
	for _, seq := range []uint64{3, 2, 1} {
		r.complete(Result{Sequence: seq, Number: 1, Ended: r.now, Observation: metrics.Observation{Status: 503}})
	}
	if len(r.retries) != 3 || len(r.records) != 3 || r.workers != 0 {
		t.Fatal("waiting capacity not retained")
	}
	for i, at := range []time.Duration{12 * time.Millisecond, 13 * time.Millisecond, 32 * time.Millisecond} {
		c.set(at)
		r.advance(c.Now())
		r.schedule()
		a := <-calls
		want := []uint64{2, 3, 1}[i]
		if a.Sequence != want || a.Number != 2 || len(r.records) != 3 || r.workers > 3 {
			t.Fatal("retry order/capacity", a)
		}
	}
	if r.overall.Initial != 3 || r.overall.Retries != 3 || len(r.retries) != 0 {
		t.Fatal("unexpected initial or ghost retry")
	}
	t.Log("three simultaneous waits retain capacity; differing/equal due dispatch 2,3,1 despite reverse completion order")
}

func TestRetryMiddleRemoval(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "force"}[force], func(t *testing.T) {
			s := retryReviewSnapshot([3]int64{1100, 1200, 1300}, [3]int64{1800, 1050, 1800})
			s.Draft.Profile.RetryAccounting = "above"
			s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 3000}, {DurationSeconds: 1, Level: 0}}
			r, c, calls := controlled(t, s)
			r.schedule()
			for i := 0; i < 3; i++ {
				<-calls
			}
			for _, seq := range []uint64{1, 2, 3} {
				r.complete(Result{Sequence: seq, Number: 1, Ended: r.now, Observation: metrics.Observation{Status: 503}})
			}
			if len(r.retries) != 3 {
				t.Fatal("three waits required")
			}
			if force {
				r.drain("user_stop", true)
			}
			c.set(1050 * time.Millisecond)
			r.advance(c.Now())
			if !force {
				r.schedule()
			}
			if force {
				if len(r.retries) != 0 || len(r.records) != 0 || r.overall.Logical[metrics.LogicalCanceled] != 3 {
					t.Fatal("force retained waits")
				}
			} else if len(r.retries) != 2 || r.records[2] != nil || r.overall.Logical[metrics.LogicalDeadline] != 1 {
				t.Fatal("middle deadline removal failed")
			}
			for _, tc := range []struct {
				at  time.Duration
				seq uint64
			}{{1100 * time.Millisecond, 1}, {1300 * time.Millisecond, 3}} {
				c.set(tc.at)
				r.advance(c.Now())
				if !force {
					r.schedule()
					a := <-calls
					if a.Sequence != tc.seq || a.Number != 2 || r.workers > 2 {
						t.Fatal("ghost/overrun", a)
					}
				}
			}
			select {
			case a := <-calls:
				t.Fatal("ghost send", a)
			default:
			}
			if r.overall.Initial != 3 || (force && r.overall.Retries != 0) || (!force && r.overall.Retries != 2) {
				t.Fatal("fabricated attempt")
			}
		})
	}
	t.Log("middle deadline preserves surviving retry sends; force releases all waiting slots without ghost attempts")
}

func TestRetryEligibilityBehavior(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		observation              metrics.Observation
		network, timeouts, retry bool
	}{
		{"network_enabled", metrics.Observation{NetworkFailure: true}, true, false, true},
		{"network_disabled", metrics.Observation{NetworkFailure: true}, false, true, false},
		{"body_enabled", metrics.Observation{Status: 200, BodyReadFailure: true}, true, false, true},
		{"body_disabled", metrics.Observation{Status: 200, BodyReadFailure: true}, false, true, false},
		{"timeout_enabled", metrics.Observation{AttemptTimeout: true}, false, true, true},
		{"timeout_disabled", metrics.Observation{AttemptTimeout: true}, true, false, false},
		{"canceled", metrics.Observation{Status: 503, Canceled: true, NetworkFailure: true}, true, true, false},
		{"unknown", metrics.Observation{Status: 503, Unknown: true, AttemptTimeout: true}, true, true, false},
		{"deadline", metrics.Observation{Status: 503, DeadlineInterruption: true, NetworkFailure: true}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.Draft.Capacity = 1
			s.Draft.Profile.RetryAccounting = "above"
			s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 1000}, {DurationSeconds: 1, Level: 0}}
			p := &s.Effective[0].Policy
			p.MaxRetries = 1
			p.DelayMS = 1100
			p.MaxDelayMS = 1500
			p.DeadlineMS = 1800
			p.Network = tc.network
			p.Timeouts = tc.timeouts
			r, c, calls := controlled(t, s)
			r.schedule()
			first := <-calls
			r.complete(Result{Sequence: first.Sequence, Number: 1, Ended: r.now, Observation: tc.observation})
			c.set(1100 * time.Millisecond)
			r.advance(c.Now())
			r.schedule()
			if tc.retry {
				a := <-calls
				if a.Sequence != first.Sequence || a.Number != 2 || string(a.Body) != string(first.Body) || r.overall.Retries != 1 || len(r.records) != 1 {
					t.Fatal("eligible retry identity/capacity", a)
				}
			} else if r.overall.Retries != 0 || len(r.records) != 0 || len(r.retries) != 0 {
				t.Fatal("terminal failure retried")
			}
			select {
			case a := <-calls:
				t.Fatal("extra send", a)
			default:
			}
			if r.overall.Initial != 1 {
				t.Fatal("zero stage fabricated initial")
			}
		})
	}
	t.Log("network/body/timeout switches drive sends; canceled/unknown/deadline veto overlapping retry triggers")
}
