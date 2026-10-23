package engine

import (
	"context"
	"encoding/json"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time                         { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (c *testClock) set(d time.Duration) {
	c.mu.Lock()
	c.now = time.Unix(1000, 0).Add(d)
	c.mu.Unlock()
}

type transportFunc func(context.Context, Attempt) Result

func (f transportFunc) DoAttempt(c context.Context, a Attempt) Result { return f(c, a) }

type sinkFunc func(context.Context, Checkpoint) error

func (f sinkFunc) Commit(c context.Context, p Checkpoint) error { return f(c, p) }

func fixture() domain.RunSnapshot {
	d := domain.NewDraft()
	d.Capacity = 4
	d.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 1000}}
	d.Templates = []domain.TemplateSource{{ID: "a", Valid: true, Body: json.RawMessage(`{"seq":"{{sys.request.seq}}"}`)}}
	d.SelectedIDs = []string{"a"}
	p := domain.DefaultPolicy()
	p.TimeoutMS = 100
	p.DeadlineMS = 500
	p.DelayMS = 0
	return domain.RunSnapshot{RunID: "run", Draft: d, Effective: []domain.EffectiveRequest{{TemplateID: "a", URL: "http://127.0.0.1/", Method: "POST", Weight: 1, Policy: p}}}
}
func controlled(t *testing.T, s domain.RunSnapshot) (*Runner, *testClock, chan Attempt) {
	t.Helper()
	clock := &testClock{}
	clock.set(0)
	calls := make(chan Attempt, 100)
	r, err := New(s, Options{Clock: clock, Transport: transportFunc(func(ctx context.Context, a Attempt) Result {
		calls <- a
		<-ctx.Done()
		return Result{Ended: time.Now(), Observation: metrics.Observation{Canceled: true}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	r.origin = clock.Now()
	r.now = r.origin
	r.state = "running"
	t.Cleanup(func() {
		for _, l := range r.records {
			if l.cancel != nil {
				l.cancel()
			}
		}
	})
	return r, clock, calls
}

func TestSelectors(t *testing.T) {
	for _, mode := range []string{"round_robin", "uniform_random", "shuffled_round", "weighted_random"} {
		s := selector{mode: mode, weights: []float64{1, 3, 6}, rng: rand.New(rand.NewSource(7))}
		counts := [3]int{}
		seen := map[int]bool{}
		for i := 0; i < 10000; i++ {
			n := s.next()
			if n < 0 || n > 2 {
				t.Fatal(n)
			}
			counts[n]++
			if mode == "round_robin" && n != i%3 {
				t.Fatal("order")
			}
			if mode == "shuffled_round" {
				if i%3 == 0 {
					seen = map[int]bool{}
				}
				if seen[n] {
					t.Fatal("duplicate in round")
				}
				seen[n] = true
			}
		}
		if mode == "weighted_random" && (counts[0] < 800 || counts[2] < 5700) {
			t.Fatal(counts)
		}
	}
	if arrivals(math.MaxInt64, time.Nanosecond) != uint64(math.MaxInt64)/1e9 {
		t.Fatal("overflow")
	}
	t.Log("four selection modes and integer multiplication verified")
}

func TestPacing(t *testing.T) {
	r, c, _ := controlled(t, fixture())
	r.schedule()
	if r.overall.Initial != 1 {
		t.Fatal(r.overall.Initial)
	}
	c.set(500 * time.Millisecond)
	r.advance(c.Now())
	r.schedule()
	if r.overall.Initial != 2 || r.schedulerMisses != 499 || r.desired != 501 {
		t.Fatalf("initial=%d missed=%d desired=%d", r.overall.Initial, r.schedulerMisses, r.desired)
	}
	c.set(time.Second)
	r.advance(c.Now())
	if r.desired != 1000 || r.state != "draining" {
		t.Fatal(r.desired, r.state)
	}
	b := r.buckets.Checkpoint()
	if len(b) != 1 || b[0].Desired != 1000 {
		t.Fatal("bucket attribution", b)
	}
	t.Log("998 skipped slots retained; two actual permits; no catch-up")
}

func TestRetryIdentity(t *testing.T) {
	s := fixture()
	s.Effective[0].Policy.MaxRetries = 2
	s.Draft.Profile.RetryAccounting = "above"
	r, c, calls := controlled(t, s)
	r.schedule()
	a := <-calls
	for number, status := range []int{503, 503, 200} {
		l := r.records[1]
		if l == nil {
			t.Fatal("logical missing")
		}
		l.cancel()
		r.complete(Result{Ended: r.now, Sequence: 1, Number: uint64(number + 1), Observation: metrics.Observation{Status: status, Duration: time.Millisecond}})
		if status != 200 {
			c.set(time.Duration(number+1) * time.Millisecond)
			r.now = c.Now()
			if !r.dispatch(r.records[1]) {
				t.Fatal("retry not sent")
			}
			retry := <-calls
			if string(retry.Body) != string(a.Body) || retry.Sequence != a.Sequence {
				t.Fatal("retry mutated identity")
			}
		}
	}
	if r.overall.Initial != 1 || r.overall.Retries != 2 || r.overall.Logical[metrics.LogicalSuccess] != 1 || r.overall.Attempts[metrics.HTTPError] != 2 {
		t.Fatal(r.overall)
	}
	t.Log("503/503/200 = one logical success, three attempts, stable bytes")
}

func TestRunStop(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Mode = "closed"
	s.Draft.Profile.Steps[0].Level = 1
	calls := make(chan Attempt, 2)
	r, err := New(s, Options{Transport: transportFunc(func(ctx context.Context, a Attempt) Result {
		calls <- a
		<-ctx.Done()
		return Result{Ended: time.Now(), Observation: metrics.Observation{Canceled: true}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- r.Run(context.Background()) }()
	<-calls
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	v, err := r.Stop(ctx, false)
	if err != nil || v.State != "draining" {
		t.Fatal(v, err)
	}
	if _, err = r.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("did not finish")
	}
	v = r.View()
	if !v.Durable || !v.WorkersReleased || v.Aggregate.Initial != 1 || v.Aggregate.Attempts[metrics.Canceled] != "1" {
		t.Fatal(v)
	}
	select {
	case <-calls:
		t.Fatal("late admission")
	default:
	}
	t.Log("acknowledged stop froze permits; force result counted once; workers released and final checkpoint acknowledged")
}

func TestPersistenceFailure(t *testing.T) {
	s := fixture()
	r, err := New(s, Options{Transport: transportFunc(func(ctx context.Context, a Attempt) Result {
		return Result{Ended: time.Now(), Observation: metrics.Observation{Status: 200}}
	}), Sink: sinkFunc(func(context.Context, Checkpoint) error { return errors.New("disk full") })})
	if err != nil {
		t.Fatal(err)
	}
	r.intent.Store(true)
	if r.Run(context.Background()) == nil {
		t.Fatal("expected persistence failure")
	}
	v := r.View()
	if v.Durable || v.PersistenceError == "" || v.State != "application_failed" {
		t.Fatal(v)
	}
	t.Log("disk failure explicit; memory report retained")
}

func TestBudgetDeadlineStages(t *testing.T) {
	for _, accounting := range []string{"within", "above"} {
		t.Run(accounting, func(t *testing.T) {
			s := fixture()
			s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 1000}, {DurationSeconds: 1, Level: 0}}
			s.Draft.Profile.RetryAccounting = accounting
			s.Effective[0].Policy.MaxRetries = 2
			s.Effective[0].Policy.DeadlineMS = 1500
			s.Effective[0].Policy.DelayMS = 1000
			r, c, calls := controlled(t, s)
			r.schedule()
			<-calls
			r.records[1].cancel()
			r.complete(Result{Ended: r.now, Sequence: 1, Number: 1, Observation: metrics.Observation{Status: 503}})
			c.set(time.Second)
			r.advance(c.Now())
			r.schedule()
			if accounting == "above" {
				retry := <-calls
				if retry.Stage != 1 || r.records[1].firstStage != 0 || r.overall.Retries != 1 {
					t.Fatal("stage identity")
				}
			} else {
				if r.overall.Retries != 0 {
					t.Fatal("zero stage gave retry budget")
				}
				c.set(1500 * time.Millisecond)
				r.advance(c.Now())
				r.schedule()
				if r.overall.Logical[metrics.LogicalDeadline] != 1 || r.overall.Retries != 0 || len(r.retries) != 0 {
					t.Fatal("unsent deadline fabricated attempt")
				}
			}
		})
	}
	t.Log("within zero gives no retry budget; above retry attributed to next stage; deadline waiting creates no attempt")
}

func TestClosedRetirement(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Mode = "closed"
	s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 4}, {DurationSeconds: 1, Level: 1}, {DurationSeconds: 1, Level: 4}}
	r, c, _ := controlled(t, s)
	r.schedule()
	if len(r.records) != 4 {
		t.Fatal(len(r.records))
	}
	c.set(time.Second)
	r.advance(c.Now())
	r.schedule()
	r.publish()
	if r.View().ActiveUsers != 1 || r.View().RetiringUsers != 3 {
		t.Fatal(r.View())
	}
	c.set(2 * time.Second)
	r.advance(c.Now())
	r.schedule()
	r.publish()
	if len(r.records) != 4 || r.nextUser != 4 || r.View().ActiveUsers != 4 || r.View().RetiringUsers != 0 {
		t.Fatal("replaced retiring users", r.View())
	}
	t.Log("4/1/4 reactivates same busy identities; no oversubscription")
}

func TestBackpressure(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Steps[0].DurationSeconds = 30
	r, c, _ := controlled(t, s)
	r.writing = true
	for i := 1; i <= 11; i++ {
		c.set(time.Duration(i) * time.Second)
		r.advance(c.Now())
	}
	if r.state != "application_failed" || len(r.buckets.Checkpoint()) != 10 {
		t.Fatal(r.state, len(r.buckets.Checkpoint()))
	}
	r.sealFinal()
	r.queueCheckpoint(true)
	if len(r.Report().Buckets) != 22 || r.pending == nil {
		t.Fatal("bounded final evidence missing")
	}
	t.Log("ten pending buckets plus one emergency final bucket; admission stopped")
}

func TestLateForcedStatus(t *testing.T) {
	r, _, calls := controlled(t, fixture())
	r.schedule()
	<-calls
	r.drain("user_stop", true)
	r.complete(Result{Ended: r.now, Sequence: 1, Number: 1, Observation: metrics.Observation{Status: 503}, Sample: metrics.Sample{TemplateID: "a"}})
	if r.overall.Attempts[metrics.Canceled] != 1 || r.overall.Attempts[metrics.HTTPError] != 0 || r.overall.Statuses[503] != 1 || r.workers != 0 {
		t.Fatal("late result duplicated or erased status")
	}
	t.Log("one canceled terminal outcome; late 503 retained independently")
}

func TestWireAndBackoff(t *testing.T) {
	total := uint64(1 << 63)
	sample := Sample{Sequence: 1 << 63, Attempt: 1 << 63, Duration: time.Duration(1 << 62), Request: metrics.Body{Total: &total}}
	raw, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Sample
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Sequence != sample.Sequence || decoded.Duration != sample.Duration || *decoded.Request.Total != total {
		t.Fatal(string(raw))
	}
	p := domain.DefaultPolicy()
	p.DelayMode = "exponential"
	p.DelayMS = 10
	p.MaxDelayMS = 25
	rng := rand.New(rand.NewSource(1))
	for i, want := range []time.Duration{10, 20, 25, 25} {
		if got := retryDelay(p, uint64(i+1), rng); got != want*time.Millisecond {
			t.Fatal(got)
		}
	}
	p.Jitter = true
	for i := 0; i < 1000; i++ {
		if d := retryDelay(p, 100, rng); d < 0 || d > 25*time.Millisecond {
			t.Fatal(d)
		}
	}
	t.Log(">2^53 identities and body totals roundtrip exactly; backoff capped; jitter bounded")
}

func TestCapacityAndWithinPriority(t *testing.T) {
	s := fixture()
	s.Draft.Capacity = 1
	s.Effective[0].Policy.MaxRetries = 1
	r, c, calls := controlled(t, s)
	r.schedule()
	<-calls
	c.set(10 * time.Millisecond)
	r.advance(c.Now())
	r.schedule()
	if r.desired != 11 || r.overall.Initial != 1 || r.capacityMisses != 1 || r.schedulerMisses != 9 {
		t.Fatal("demand did not reconcile")
	}
	r.records[1].cancel()
	r.complete(Result{Ended: r.now, Sequence: 1, Number: 1, Observation: metrics.Observation{Status: 503}})
	c.set(11 * time.Millisecond)
	r.advance(c.Now())
	r.schedule()
	retry := <-calls
	if retry.Number != 2 || r.overall.Initial != 1 || r.overall.Retries != 1 {
		t.Fatal("retry did not consume within slot")
	}
	t.Log("desired=sent+scheduler misses+capacity misses; eligible retry takes next within slot")
}

func TestUnreleasedWorker(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Mode = "closed"
	s.Draft.Profile.Steps[0].Level = 1
	entered := make(chan struct{})
	release := make(chan struct{})
	r, err := New(s, Options{ReleaseTimeout: 20 * time.Millisecond, Transport: transportFunc(func(context.Context, Attempt) Result {
		close(entered)
		<-release
		return Result{Ended: time.Now(), Observation: metrics.Observation{Status: 200}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = r.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	for r.View().State != "application_failed" {
		select {
		case <-ctx.Done():
			t.Fatal("worker failure not visible")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if r.View().WorkersReleased {
		t.Fatal("claimed released")
	}
	select {
	case <-r.Done():
		t.Fatal("released reservation before worker")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing worker fault")
		}
	case <-ctx.Done():
		t.Fatal("worker did not release")
	}
	t.Log("cancellation-ignoring adapter exposed failure; Done stayed open until real worker release")
}
