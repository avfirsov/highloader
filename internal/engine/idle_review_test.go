package engine

import (
	"context"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math"
	"testing"
	"time"
)

func TestUsefulQuantumStarts(t *testing.T) {
	for _, level := range []int64{1, 7, 333, 999} {
		for ms := int64(0); ms < 2000; ms++ {
			elapsed := time.Duration(ms)*time.Millisecond + 350*time.Microsecond
			current := arrivals(level, time.Duration(ms+1)*time.Millisecond)
			next := ms + 1
			for arrivals(level, time.Duration(next+1)*time.Millisecond) == current {
				next++
			}
			want := time.Duration(next)*time.Millisecond - elapsed
			if got := nextBudgetDelay(elapsed, level); got != want {
				t.Fatalf("level=%d elapsed=%s got=%s want=%s", level, elapsed, got, want)
			}
		}
		if got := nextBudgetDelay(time.Duration(math.MaxInt64), level); got <= 0 || got > time.Second {
			t.Fatal("overflow", got)
		}
	}
	if nextBudgetDelay(0, 1) != 999*time.Millisecond {
		t.Fatal("1RPS permit belongs to quantum999")
	}
	t.Log("1,7,333,999RPS agree with cumulative demand across two seconds; duration maximum stays bounded")
}

func TestIdleWakeCaps(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 1}, {DurationSeconds: 1, Level: 7}}
	r, c, _ := controlled(t, s)
	r.schedule()
	if got := r.wakeDelay(c.Now(), r.origin); got != 100*time.Millisecond {
		t.Fatal("publication cap", got)
	}
	c.set(950 * time.Millisecond)
	r.advance(c.Now())
	r.schedule()
	if got := r.wakeDelay(c.Now(), r.origin.Add(940*time.Millisecond)); got != 49*time.Millisecond {
		t.Fatal("useful quantum", got)
	}
	// At the already processed last quantum, the next permit is in a later second;
	// bucket/stage/profile boundaries must win even with a future publication.
	c.set(999 * time.Millisecond)
	r.advance(c.Now())
	r.elapsed = 999 * time.Millisecond
	if got := r.wakeDelay(c.Now(), c.Now()); got != time.Millisecond {
		t.Fatal("stage/bucket cap", got)
	}
	c.set(1000 * time.Millisecond)
	r.advance(c.Now())
	r.schedule()
	if r.stage != 1 || r.wakeDelay(c.Now(), c.Now()) != 100*time.Millisecond {
		t.Fatal("stage transition")
	}
	for _, guard := range []string{"records", "workers", "canceled", "closed", "zero", "high", "draining"} {
		t.Run(guard, func(t *testing.T) {
			r, c, _ := controlled(t, s)
			r.schedule()
			c.set(1350 * time.Microsecond)
			r.advance(c.Now())
			r.schedule()
			switch guard {
			case "records":
				r.records[1] = &logical{}
			case "workers":
				r.workers = 1
			case "canceled":
				r.canceled[1] = &logical{}
			case "closed":
				r.snapshot.Draft.Profile.Mode = "closed"
			case "zero":
				r.snapshot.Draft.Profile.Steps[0].Level = 0
			case "high":
				r.snapshot.Draft.Profile.Steps[0].Level = 1000
			case "draining":
				r.state = "draining"
			}
			if got := r.wakeDelay(c.Now(), r.origin); got != 650*time.Microsecond {
				t.Fatal("guard failed", got)
			}
		})
	}
	t.Log("useful quantum capped by publication/stage/bucket; all busy,mode,level,lifecycle guards retain aligned wakes")
}

func TestIdleRunWake(t *testing.T) {
	c := &phaseClock{arms: make(chan phaseArm, 64)}
	c.set(0)
	s := fixture()
	s.Draft.Profile.Steps[0].Level = 1
	calls := make(chan Attempt, 4)
	r, err := New(s, Options{Clock: c, Transport: transportFunc(func(ctx context.Context, a Attempt) Result {
		calls <- a
		<-ctx.Done()
		return Result{Ended: c.Now(), Observation: metrics.Observation{Canceled: true}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	r.acks = make(chan acknowledgement)
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
			t.Error("run did not stop")
		}
	})
	a := awaitArm(t, c)
	c.set(time.Millisecond)
	a.wake <- c.Now()
	a = awaitArm(t, c)
	if a.delay != 99*time.Millisecond {
		t.Fatal("first useful cap", a.delay)
	}
	// Unbuffered sends prove each preceding acknowledgment has been consumed.
	for i := 0; i < 8; i++ {
		select {
		case r.acks <- acknowledgement{}:
		case <-time.After(time.Second):
			t.Fatal("ack stalled")
		}
	}
	if len(c.arms) != 0 {
		t.Fatal("early acknowledgments replaced pending timer")
	}
	for ms := 100; ms <= 900; ms += 100 {
		c.set(time.Duration(ms) * time.Millisecond)
		a.wake <- c.Now()
		a = awaitArm(t, c)
		want := 100 * time.Millisecond
		if ms == 900 {
			want = 99 * time.Millisecond
		}
		if a.delay != want {
			t.Fatalf("at%d delay%s want%s", ms, a.delay, want)
		}
		select {
		case got := <-calls:
			t.Fatal("early permit", got)
		default:
		}
	}
	c.set(999 * time.Millisecond)
	a.wake <- c.Now()
	a = awaitArm(t, c)
	select {
	case got := <-calls:
		if got.Started.Sub(time.Unix(1000, 0)) != 999*time.Millisecond {
			t.Fatal(got.Started)
		}
	case <-time.After(time.Second):
		t.Fatal("missing999ms permit")
	}
	if a.delay != time.Millisecond {
		t.Fatal("busy state retained idle delay", a.delay)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if _, err := r.Stop(stopCtx, true); err != nil {
		t.Fatal(err)
	}
	t.Log("early acks allocate no timers; publish caps100ms; first permit999ms; busy wake1ms; stop selectable")
}

func TestWakeSelectPaths(t *testing.T) {
	for _, top := range []bool{true, false} {
		t.Run(map[bool]string{true: "top_ready", false: "bottom_blocked"}[top], func(t *testing.T) {
			c := &phaseClock{arms: make(chan phaseArm, 8)}
			c.set(0)
			if top {
				first := true
				c.onArm = func(a phaseArm) {
					if first {
						first = false
						c.set(1350 * time.Microsecond)
						a.wake <- c.Now()
					}
				}
			}
			calls := make(chan Attempt, 4)
			r, err := New(fixture(), Options{Clock: c, Transport: transportFunc(func(ctx context.Context, a Attempt) Result {
				calls <- a
				<-ctx.Done()
				return Result{Ended: c.Now(), Observation: metrics.Observation{Canceled: true}}
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
					t.Error("run did not stop")
				}
			})
			a := awaitArm(t, c)
			if !top {
				// Transport cannot start until the first top-select default completed.
				select {
				case <-calls:
				case <-time.After(time.Second):
					t.Fatal("first admission stalled")
				}
				c.set(1350 * time.Microsecond)
				a.wake <- c.Now()
			}
			a = awaitArm(t, c)
			if a.delay != 650*time.Microsecond {
				t.Fatal("wrong consume rearm", a.delay)
			}
		})
	}
	t.Log("ready top and blocked bottom wakes both preserve origin phase")
}
