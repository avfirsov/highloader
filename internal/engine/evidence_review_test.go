package engine

import (
	"context"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventTime(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Steps[0].DurationSeconds = 3
	s.Effective[0].Policy.TimeoutMS = 2000
	s.Effective[0].Policy.DeadlineMS = 2500
	r, c, calls := controlled(t, s)
	r.schedule()
	<-calls
	c.set(1001 * time.Millisecond)
	r.advance(c.Now())
	r.acknowledge(<-r.acks)
	if len(r.buckets.Checkpoint()) != 0 {
		t.Fatal("base not evicted")
	}
	r.complete(Result{Sequence: 1, Number: 1, Ended: r.origin.Add(999 * time.Millisecond), Observation: metrics.Observation{Status: 200, Duration: 10 * time.Second}})
	if got := r.overall.LogicalDurations[metrics.LogicalSuccess].Summary(); got.MeanNS == nil || *got.MeanNS != float64(999*time.Millisecond) {
		t.Fatal(got)
	}
	r.flushCompletion(false)
	if len(r.completionBatches) != 1 {
		t.Fatal("missing batch")
	}
	for _, d := range r.completionBatches[0].Deltas {
		if d.Index != 0 || d.Value.Attempts[metrics.Success] != 1 || d.Value.Logical[metrics.LogicalSuccess] != 1 {
			t.Fatal(d)
		}
	}
	points := r.livePoints()
	if len(points) != 1 || points[0].Completions != 1 || points[0].P50 == nil || *points[0].P50 < 999*time.Millisecond {
		t.Fatal(points)
	}
	t.Log("durable base0 evicted; late999ms completion updates delta/live0 once; logical latency excludes queue")
}

func TestDelayedFailureDecision(t *testing.T) {
	for _, retry := range []bool{false, true} {
		s := fixture()
		s.Effective[0].Policy.TimeoutMS = 500
		s.Effective[0].Policy.DeadlineMS = 500
		if retry {
			s.Effective[0].Policy.MaxRetries = 1
		}
		r, c, calls := controlled(t, s)
		r.schedule()
		<-calls
		c.set(600 * time.Millisecond)
		r.advance(c.Now())
		r.complete(Result{Sequence: 1, Number: 1, Ended: r.origin.Add(100 * time.Millisecond), Observation: metrics.Observation{Status: 503}})
		outcome := metrics.LogicalFailure
		want := 100 * time.Millisecond
		if retry {
			outcome = metrics.LogicalDeadline
			want = 500 * time.Millisecond
		}
		summary := r.overall.LogicalDurations[outcome].Summary()
		if r.overall.Logical[outcome] != 1 || r.overall.Retries != 0 || summary.MeanNS == nil || *summary.MeanNS != float64(want) {
			t.Fatal(retry, r.overall.Logical, summary)
		}
	}
	t.Log("exhausted failure ends100ms; queued eligible retry expires500ms without send")
}

func TestWriterJoin(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 0}, {DurationSeconds: 30, Level: 1}}
	clock := &testClock{}
	clock.set(0)
	entered := make(chan struct{})
	release := make(chan struct{})
	var commits atomic.Int32
	r, err := New(s, Options{Clock: clock, CommitTimeout: 20 * time.Millisecond, Transport: transportFunc(func(context.Context, Attempt) Result {
		return Result{Ended: clock.Now(), Observation: metrics.Observation{Status: 200}}
	}), Sink: sinkFunc(func(context.Context, Checkpoint) error { commits.Add(1); close(entered); <-release; return nil })})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(context.Background()) }()
	deadline := time.After(time.Second)
	for r.View().State == "preparing" {
		select {
		case <-deadline:
			t.Fatal("start")
		case <-time.After(time.Millisecond):
		}
	}
	clock.set(time.Second)
	<-entered
	for r.View().PersistenceError == "" {
		select {
		case <-deadline:
			t.Fatal("watchdog")
		case <-time.After(time.Millisecond):
		}
	}
	if r.View().WriterReleased {
		t.Fatal("watchdog released writer")
	}
	select {
	case <-r.Done():
		t.Fatal("Done closed before Commit returned")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing watchdog fault")
		}
	case <-deadline:
		t.Fatal("join")
	}
	if commits.Load() != 1 || !r.View().WriterReleased || r.View().DurableSequence == 0 || r.View().Durable {
		t.Fatal(r.View(), commits.Load())
	}
	t.Log("watchdog visible; Done waits actual Commit; late success retains durable cut without hiding fault")
}

func TestCompletionBatches(t *testing.T) {
	r, _, _ := controlled(t, fixture())
	outcome := metrics.Success
	for i := 0; i < 10; i++ {
		r.recordCompletion("a", r.origin, &outcome, nil, time.Millisecond)
		r.flushCompletion(false)
	}
	first := clone(r.completionBatches[0])
	r.recordCompletion("a", r.origin, &outcome, nil, 2*time.Millisecond)
	r.flushCompletion(false)
	if len(r.completionBatches) != 10 || len(r.completion) == 0 || r.state != "application_failed" {
		t.Fatal("batch pressure")
	}
	if first.Deltas[0].Value.Attempts[0] != r.completionBatches[0].Deltas[0].Value.Attempts[0] {
		t.Fatal("mutated emitted batch")
	}
	r.flushCompletion(true)
	if len(r.completionBatches) != 11 || len(r.completion) != 0 {
		t.Fatal("emergency batch")
	}
	var merged CompletionValue
	for _, batch := range r.completionBatches {
		if err := merged.Merge(batch.Deltas[0].Value); err != nil {
			t.Fatal(err)
		}
	}
	if merged.Attempts[0] != 11 {
		t.Fatal("lost deltas")
	}
	var full CompletionValue
	full.Attempts[metrics.Canceled] = Counter(math.MaxUint64)
	other := CompletionValue{}
	other.Attempts[metrics.Canceled] = 1
	if full.Merge(other) != metrics.ErrOverflow || full.Attempts[metrics.Canceled] != Counter(math.MaxUint64) {
		t.Fatal("partial overflow mutation")
	}
	t.Log("ten immutable batches + one emergency; checked transactional merge retains11 events")
}

type cpuFixture struct{ value CPUValue }

func (c *cpuFixture) Latest() CPUValue { return c.value }

func TestCPUUsers(t *testing.T) {
	s := fixture()
	s.Draft.Profile.Mode = "closed"
	s.Draft.Profile.Steps[0].Level = 2
	r, c, _ := controlled(t, s)
	percent := 37.0
	source := &cpuFixture{CPUValue{ID: "1", At: r.origin, Percent: &percent}}
	r.options.CPU = source
	r.schedule()
	r.advance(c.Now())
	for i := 0; i < 10; i++ {
		r.sampleCPU()
		r.publish()
	}
	if r.cpuStats.Valid != 1 || r.cpuStats.Sum != 37 {
		t.Fatal(r.cpuStats)
	}
	r.captureBase()
	meta := r.baseMeta[0]
	if meta.TargetUsers == nil || *meta.ActiveUsers != 2 {
		t.Fatal(meta)
	}
	c.set(time.Second)
	r.advance(c.Now())
	meta = r.baseMeta[1]
	if meta.CPU != nil || meta.CPUReason == "" {
		t.Fatal("stale CPU filled gap")
	}
	source.value = CPUValue{ID: "2", At: c.Now(), Reason: "PDH unavailable"}
	r.sampleCPU()
	r.captureBase()
	if r.cpuStats.Missing != 1 {
		t.Fatal(r.cpuStats)
	}
	if r.baseMeta[1].TargetUsers == nil || *r.baseMeta[1].TargetUsers != 0 || *r.baseMeta[1].RetiringUsers != 2 {
		t.Fatal(r.baseMeta[1])
	}
	t.Log("repeated identity counts once; stale interval gap; missing reason and drain retirees preserved")
}

type movingRandom struct {
	clock *testClock
	moved bool
}

func (m *movingRandom) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i)
	}
	if !m.moved {
		m.clock.set(time.Millisecond)
		m.moved = true
	}
	return len(p), nil
}

func TestRejectedRenderSelection(t *testing.T) {
	s := fixture()
	s.Draft.Templates[0].Body = json.RawMessage(`{"id":"{{sys.uuid}}"}`)
	second := s.Draft.Templates[0]
	second.ID = "b"
	s.Draft.Templates = append(s.Draft.Templates, second)
	e := s.Effective[0]
	e.TemplateID = "b"
	s.Effective = append(s.Effective, e)
	r, c, calls := controlled(t, s)
	r.options.TokenRandom = &movingRandom{clock: c}
	r.schedule()
	if r.sequence != 0 || r.overall.Started != 0 || r.schedulerMisses != 1 {
		t.Fatal("rejected candidate committed identity")
	}
	r.advance(c.Now())
	r.schedule()
	a := <-calls
	if a.Sequence != 1 || a.Request.TemplateID != "a" || r.desired != 2 {
		t.Fatal(a)
	}
	t.Log("expired render offer consumes no sequence or round-robin selection")
}

func TestCanceledRecoveryMarker(t *testing.T) {
	r, _, calls := controlled(t, fixture())
	r.schedule()
	<-calls
	r.drain("user_stop", true)
	r.flushCompletion(false)
	cp := r.checkpoint(false)
	if len(cp.Unfinished) != 1 || !cp.Unfinished[0].OutcomeCommitted || r.overall.Attempts[metrics.Canceled] != 1 || r.overall.Logical[metrics.LogicalCanceled] != 1 {
		t.Fatal(cp)
	}
	if cp.Final {
		t.Fatal("early final")
	}
	raw, _ := json.Marshal(cp)
	if !strings.Contains(string(raw), `"outcomeCommitted":true`) {
		t.Fatal(string(raw))
	}
	t.Log("releasing tombstone explicitly marks already committed attempt/logical cancellation")
}

type arrivalClock struct {
	calls  int
	now    time.Time
	onRead func(int)
}

func (c *arrivalClock) Now() time.Time {
	c.calls++
	old := c.now
	if c.onRead != nil {
		c.onRead(c.calls)
	}
	return old
}
func (c *arrivalClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func TestConsumeClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	origin := time.Unix(1000, 0)
	clock := &arrivalClock{now: origin}
	r, err := New(fixture(), Options{Clock: clock, Transport: transportFunc(func(context.Context, Attempt) Result { panic("unexpected I/O") })})
	if err != nil {
		t.Fatal(err)
	}
	l := &logical{seq: 1, index: 0, active: true, attempt: 1, start: origin, deadline: origin.Add(time.Second), heapIndex: -1, permit: Attempt{Started: origin, Timeout: time.Second}}
	l.cancel = func() {}
	for _, a := range r.logicalAggregates(l) {
		if err := a.StartLogical(); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range r.attemptAggregates(l) {
		if err := a.Admit(false); err != nil {
			t.Fatal(err)
		}
	}
	r.records[1] = l
	r.livePermits[1] = 1
	r.workers = 1
	clock.onRead = func(n int) {
		if n == 2 {
			clock.now = origin.Add(time.Millisecond)
			r.results <- Result{Sequence: 1, Number: 1, Ended: clock.now, Observation: metrics.Observation{Status: 200}}
		}
		if n == 3 {
			r.intent.Store(true)
			cancel()
		}
	}
	if err = r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if r.overall.Attempts[metrics.Success] != 1 || r.overall.Attempts[metrics.Unknown] != 0 || r.overall.LogicalDurations[metrics.LogicalSuccess].Summary().Count != 1 {
		t.Fatal(r.overall)
	}
	t.Log("valid event newer than top clock sample accepted after consume-time refresh")
}

type drawRandom struct{ calls int }

func (d *drawRandom) Intn(n int) int       { d.calls++; return d.calls % n }
func (d *drawRandom) Int63n(n int64) int64 { d.calls++; return int64(d.calls) % n }
func (d *drawRandom) Float64() float64 {
	d.calls++
	if d.calls%2 == 1 {
		return .75
	}
	return .25
}

func TestPendingRandomSelection(t *testing.T) {
	for _, mode := range []string{"uniform_random", "weighted_random", "shuffled_round"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture()
			s.Draft.SelectionMode = mode
			s.Draft.Templates[0].Body = json.RawMessage(`{"id":"{{sys.uuid}}"}`)
			second := s.Draft.Templates[0]
			second.ID = "b"
			s.Draft.Templates = append(s.Draft.Templates, second)
			e := s.Effective[0]
			e.TemplateID = "b"
			s.Effective = append(s.Effective, e)
			r, c, calls := controlled(t, s)
			draws := &drawRandom{}
			r.selector.rng = draws
			r.options.TokenRandom = &movingRandom{clock: c}
			r.schedule()
			if r.pendingSelection == nil || r.sequence != 0 {
				t.Fatal("missing pending candidate")
			}
			expected := s.Effective[*r.pendingSelection].TemplateID
			count := draws.calls
			r.advance(c.Now())
			r.schedule()
			a := <-calls
			if a.Request.TemplateID != expected || a.Sequence != 1 || draws.calls != count || r.pendingSelection != nil {
				t.Fatal(a, draws.calls, count)
			}
			if mode == "shuffled_round" {
				next := r.selector.next()
				if s.Effective[next].TemplateID == expected || draws.calls != count {
					t.Fatal("refilled or repeated shuffled cycle")
				}
			}
		})
	}
	t.Log("rejected uniform/weighted/shuffle candidate retained without extra draw; shuffled cycle preserved")
}

func TestCPUBoundaryAmendment(t *testing.T) {
	t.Run("multi_interval_jump", func(t *testing.T) {
		r, c, _ := controlled(t, fixture())
		percent := 19.0
		r.options.CPU = &cpuFixture{value: CPUValue{ID: "jump", At: r.origin.Add(4999 * time.Millisecond), Percent: &percent}}
		c.set(5001 * time.Millisecond)
		r.advance(c.Now())
		meta := r.baseMeta[4]
		if meta.CPU == nil || *meta.CPU != 19 || !meta.SnapshotAt.IsZero() || meta.TargetUsers != nil || r.baseMeta[5].CPU != nil || r.cpuStats.Valid != 1 || len(r.cpuAmendments) != 0 {
			t.Fatal("jump lost CPU or fabricated user state", meta, r.cpuStats)
		}
		if len(r.baseMeta) > 2 || len(r.liveMeta) > 2 {
			t.Fatal("jump allocated per-skipped-interval metadata")
		}
	})
	r, c, _ := controlled(t, fixture())
	source := &cpuFixture{}
	r.options.CPU = source
	r.captureBase()
	snapshotAt := r.baseMeta[0].SnapshotAt
	percent := 37.0
	source.value = CPUValue{ID: "boundary", At: r.origin.Add(999 * time.Millisecond), Percent: &percent}
	c.set(1001 * time.Millisecond)
	r.advance(c.Now())
	if r.baseMeta[0].CPU == nil || *r.baseMeta[0].CPU != 37 || !r.baseMeta[0].SnapshotAt.Equal(snapshotAt) || r.baseMeta[1].CPU != nil || len(r.cpuAmendments) != 0 || r.cpuStats.Valid != 1 {
		t.Fatal(r.baseMeta, r.cpuAmendments, r.cpuStats)
	}
	ack := <-r.acks
	r.acknowledge(ack)
	source.value = CPUValue{ID: "late", At: r.origin.Add(999*time.Millisecond + time.Microsecond), Percent: &percent}
	r.sampleCPU()
	cp := r.checkpoint(false)
	if len(cp.CPUAmendments) != 1 || len(r.baseMeta) != 1 || cp.CPUAmendments[0].Index != 0 || r.cpuStats.Valid != 2 {
		t.Fatal(cp.CPUAmendments, r.baseMeta, r.cpuStats)
	}
	source.value = CPUValue{ID: "later", At: r.origin.Add(999*time.Millisecond + 2*time.Microsecond), Percent: &percent}
	r.sampleCPU()
	r.acknowledge(acknowledgement{checkpoint: cp})
	if len(r.cpuAmendments) != 1 {
		t.Fatal("older acknowledgement removed newer sample")
	}
	cp = r.checkpoint(false)
	r.acknowledge(acknowledgement{checkpoint: cp})
	if len(r.cpuAmendments) != 0 {
		t.Fatal("matching acknowledgement not pruned")
	}
	for i := int64(2); i < 14; i++ {
		r.cpuAmendments[i] = CPUAmendment{Index: i}
	}
	source.value = CPUValue{ID: "overflow", At: r.origin.Add(time.Second), Percent: &percent}
	r.buckets.Current().Index = 14
	r.sampleCPU()
	if r.state != "application_failed" || r.cpuStats.Valid != 3 || len(r.cpuAmendments) != 12 {
		t.Fatal("CPU amendment pressure not explicit", r.state, r.cpuStats)
	}
	t.Log("preseal placement preserves user timestamp; late CPU-only amendments survive old ack; bounded pressure fails explicitly")
}
