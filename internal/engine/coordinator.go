package engine

import (
	"container/heap"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"highloader/internal/template"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type wallClock struct{}

func (wallClock) Now() time.Time                         { return time.Now() }
func (wallClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type command struct {
	force bool
	reply chan View
}
type acknowledgement struct {
	checkpoint Checkpoint
	err        error
	timedOut   bool
}
type logical struct {
	heapIndex                       int
	seq                             uint64
	index, firstStage, attemptStage int
	user                            uint64
	body                            []byte
	start, deadline, due            time.Time
	attempt                         uint64
	active                          bool
	cancel                          context.CancelFunc
	permit                          Attempt
}
type user struct {
	id             uint64
	busy, retiring bool
}
type Runner struct {
	snapshot                                                          domain.RunSnapshot
	options                                                           Options
	programs                                                          []*template.Program
	selector                                                          selector
	pendingSelection                                                  *int
	started                                                           atomic.Bool
	intent                                                            atomic.Bool
	commands                                                          chan command
	results                                                           chan Result
	acks                                                              chan acknowledgement
	done                                                              chan struct{}
	mu                                                                sync.RWMutex
	view                                                              View
	report                                                            Checkpoint
	origin, now                                                       time.Time
	elapsed, total                                                    time.Duration
	stage                                                             int
	stageStart                                                        time.Duration
	retries                                                           retryHeap
	lastQuantum                                                       int64
	lastDesired                                                       uint64
	state, reason                                                     string
	sequence, checkpointSeq, desired, schedulerMisses, capacityMisses uint64
	records                                                           map[uint64]*logical
	canceled                                                          map[uint64]*logical
	users                                                             []*user
	nextUser                                                          uint64
	workers                                                           int
	livePermits                                                       map[uint64]uint64
	overall                                                           metrics.Aggregate
	templates                                                         []metrics.Aggregate
	stages                                                            map[int]*metrics.Aggregate
	buckets                                                           *metrics.Buckets
	templateBuckets                                                   []*metrics.Buckets
	emergencyBucket                                                   *metrics.Bucket
	samples                                                           *metrics.Samples
	durableSeq                                                        uint64
	durableCut                                                        time.Time
	persistErr                                                        string
	writing                                                           bool
	pending                                                           *Checkpoint
	finalQueued                                                       bool
	forcedAt                                                          time.Time
	permitUntil                                                       time.Time
	runErr                                                            error
	completion                                                        map[completionKey]*CompletionValue
	completionBatches                                                 []CompletionBatch
	completionSeq, completionAck                                      uint64
	completionLimit                                                   int
	completionPressure                                                bool
	liveCompletion                                                    map[int64]*CompletionValue
	baseMeta                                                          map[int64]Bucket
	liveMeta                                                          map[int64]Bucket
	cpuStats                                                          CPUSummary
	lastCPU                                                           CPUValue
	cpuDiagnostic                                                     string
	cpuAmendments                                                     map[int64]CPUAmendment
	writerTimedOut                                                    bool
	activeWriteSeq                                                    uint64
}

func New(snapshot domain.RunSnapshot, o Options) (*Runner, error) {
	if snapshot.RunID == "" || len(snapshot.Effective) == 0 || snapshot.Draft.Capacity < 1 || o.Transport == nil {
		return nil, errors.New("invalid engine configuration")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	if o.Clock == nil {
		o.Clock = wallClock{}
	}
	if o.Random == nil {
		o.Random = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	if o.TokenRandom == nil {
		o.TokenRandom = cryptorand.Reader
	}
	if o.CommitTimeout <= 0 {
		o.CommitTimeout = 5 * time.Second
	}
	if o.ReleaseTimeout <= 0 {
		o.ReleaseTimeout = 2 * time.Second
	}
	b, _ := metrics.NewBuckets(10, 600)
	s, err := metrics.NewSamples(int(snapshot.Draft.Samples.Success), int(snapshot.Draft.Samples.Failure), int(snapshot.Draft.Samples.BodyCapBytes))
	if err != nil {
		return nil, err
	}
	r := &Runner{snapshot: snapshot, options: o, commands: make(chan command, 32), results: make(chan Result, int(snapshot.Draft.Capacity)), acks: make(chan acknowledgement, 1), done: make(chan struct{}), records: map[uint64]*logical{}, livePermits: map[uint64]uint64{}, canceled: map[uint64]*logical{}, stages: map[int]*metrics.Aggregate{}, templates: make([]metrics.Aggregate, len(snapshot.Effective)), buckets: b, samples: s, state: "preparing", lastQuantum: -1}
	r.selector = selector{mode: snapshot.Draft.SelectionMode, rng: o.Random}
	r.completion = map[completionKey]*CompletionValue{}
	r.liveCompletion = map[int64]*CompletionValue{}
	r.baseMeta = map[int64]Bucket{}
	r.liveMeta = map[int64]Bucket{}
	r.cpuAmendments = map[int64]CPUAmendment{}
	r.completionLimit = 2 * (int(snapshot.Draft.Capacity) + 2*len(snapshot.Effective) + 2)
	sources := map[string]domain.TemplateSource{}
	for _, t := range snapshot.Draft.Templates {
		sources[t.ID] = t
	}
	for _, e := range snapshot.Effective {
		tb, _ := metrics.NewBuckets(10, 1)
		r.templateBuckets = append(r.templateBuckets, tb)
		p, err := template.Compile(sources[e.TemplateID].Body)
		if err != nil {
			return nil, err
		}
		r.programs = append(r.programs, p)
		r.selector.weights = append(r.selector.weights, e.Weight)
	}
	for _, step := range snapshot.Draft.Profile.Steps {
		r.total += time.Duration(step.DurationSeconds) * time.Second
	}
	// Even the preparing view owns its chart semantics; a next-run draft cannot supply them.
	r.view = View{RunID: snapshot.RunID, Mode: snapshot.Draft.Profile.Mode, RetryAccounting: snapshot.Draft.Profile.RetryAccounting, State: "preparing", Capacity: int(snapshot.Draft.Capacity), WorkersReleased: true}
	return r, nil
}

func (r *Runner) View() View            { r.mu.RLock(); defer r.mu.RUnlock(); return clone(r.view) }
func (r *Runner) Report() Checkpoint    { r.mu.RLock(); defer r.mu.RUnlock(); return clone(r.report) }
func (r *Runner) Done() <-chan struct{} { return r.done }
func clone[T any](v T) T {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var out T
	if e = json.Unmarshal(b, &out); e != nil {
		panic(e)
	}
	return out
}

func (r *Runner) Stop(ctx context.Context, force bool) (View, error) {
	r.intent.Store(true)
	c := command{force: force, reply: make(chan View, 1)}
	select {
	case <-r.done:
		return r.View(), nil
	case r.commands <- c:
	case <-ctx.Done():
		return r.View(), ctx.Err()
	default:
		return r.View(), errors.New("engine command mailbox busy")
	}
	select {
	case v := <-c.reply:
		return v, nil
	case <-r.done:
		return r.View(), nil
	case <-ctx.Done():
		return r.View(), ctx.Err()
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errors.New("runner already started")
	}
	defer close(r.done)
	r.origin = r.options.Clock.Now()
	r.now = r.origin
	r.state = "running"
	r.publish()
	wake := r.options.Clock.After(time.Millisecond)
	shutdown := ctx.Done()
	lastPublish := r.now
	for {
		r.advance(r.options.Clock.Now())
		if r.intent.Load() && r.state == "running" {
			r.drain("user_stop", false)
		}
		select {
		case c := <-r.commands:
			r.advance(r.options.Clock.Now())
			r.drain("user_stop", c.force)
			r.publish()
			c.reply <- r.View()
		case result := <-r.results:
			r.advance(r.options.Clock.Now())
			r.complete(result)
		case ack := <-r.acks:
			r.advance(r.options.Clock.Now())
			r.acknowledge(ack)
		case <-shutdown:
			r.advance(r.options.Clock.Now())
			r.drain("application_shutdown", true)
			shutdown = nil
		case <-wake:
			wake = nil
			r.advance(r.options.Clock.Now())
		default:
		}
		if r.state == "running" {
			r.schedule()
		}
		if !r.forcedAt.IsZero() && r.workers > 0 && r.now.Sub(r.forcedAt) >= r.options.ReleaseTimeout {
			r.fail("worker cancellation release timeout")
		}
		if r.state != "running" && len(r.records) == 0 && !r.finalQueued && r.workers == 0 && !r.writing {
			if r.state != "application_failed" {
				if r.reason == "profile_end" {
					r.state = "completed"
				} else {
					r.state = "stopped"
				}
			}
			r.sealFinal()
			r.queueCheckpoint(true)
			r.finalQueued = true
			r.publish()
		}
		if r.now.Sub(lastPublish) >= 100*time.Millisecond {
			r.publish()
			lastPublish = r.now
		}
		if r.finalQueued && r.workers == 0 && !r.writing && (r.pending == nil || r.persistErr != "") {
			r.publish()
			return r.runErr
		}
		if wake == nil {
			wake = r.options.Clock.After(r.wakeDelay(r.options.Clock.Now(), lastPublish))
		}
		// Block when no event is immediately ready; timer channels are injected.
		select {
		case c := <-r.commands:
			r.advance(r.options.Clock.Now())
			r.drain("user_stop", c.force)
			r.publish()
			c.reply <- r.View()
		case result := <-r.results:
			r.advance(r.options.Clock.Now())
			r.complete(result)
		case ack := <-r.acks:
			r.advance(r.options.Clock.Now())
			r.acknowledge(ack)
		case <-shutdown:
			r.advance(r.options.Clock.Now())
			r.drain("application_shutdown", true)
			shutdown = nil
		case <-wake:
			wake = nil
		}
	}
}

func (r *Runner) advance(now time.Time) {
	if now.Before(r.now) {
		return
	}
	r.now = now
	r.elapsed = now.Sub(r.origin)
	r.sampleCPU()
	for index := range r.liveCompletion {
		if index < int64(r.elapsed/time.Second)-599 {
			delete(r.liveCompletion, index)
		}
	}
	for index := range r.liveMeta {
		if index < int64(r.elapsed/time.Second)-599 {
			delete(r.liveMeta, index)
		}
	}
	if r.finalQueued {
		return
	}
	for r.elapsed-r.buckets.Current().Start >= time.Second {
		boundary := r.buckets.Current().Start + time.Second
		r.accountSkipped(boundary)
		if err := r.buckets.Seal(boundary); err != nil {
			r.fail("checkpoint bucket backpressure")
			break
		}
		for _, b := range r.templateBuckets {
			if err := b.Seal(boundary); err != nil {
				r.fail("template bucket backpressure")
				break
			}
		}
		r.queueCheckpoint(false)
	}
	r.accountSkipped(r.elapsed / time.Millisecond * time.Millisecond)
	if r.state != "running" {
		r.captureBase()
		return
	}
	if r.elapsed >= r.total {
		r.drain("profile_end", false)
		return
	}
	for r.stage < len(r.snapshot.Draft.Profile.Steps)-1 && r.elapsed >= r.stageStart+time.Duration(r.snapshot.Draft.Profile.Steps[r.stage].DurationSeconds)*time.Second {
		r.stageStart += time.Duration(r.snapshot.Draft.Profile.Steps[r.stage].DurationSeconds) * time.Second
		r.stage++
		r.lastQuantum = -1
	}
	r.captureBase()
}

func (r *Runner) schedule() {
	for _, l := range r.records {
		if !l.active && !r.now.Before(l.deadline) {
			r.finish(l, metrics.LogicalDeadline)
		}
	}
	if r.snapshot.Draft.Profile.Mode == "closed" {
		r.scaleUsers()
		for l := r.nextRetry(); l != nil; l = r.nextRetry() {
			if !r.dispatch(l) {
				break
			}
		}
		for _, u := range r.users {
			if !u.busy && !u.retiring {
				r.admit(u.id)
			}
		}
		return
	}
	if r.snapshot.Draft.Profile.RetryAccounting == "above" {
		for l := r.nextRetry(); l != nil; l = r.nextRetry() {
			if !r.dispatch(l) {
				break
			}
		}
	}
	offset := r.elapsed - r.stageStart
	q := int64(offset / time.Millisecond)
	if q == r.lastQuantum {
		return
	}
	r.lastQuantum = q
	quantumEnd := r.stageStart + time.Duration(q+1)*time.Millisecond
	want := r.demandAt(quantumEnd)
	slots := want - r.lastDesired
	r.desired += slots
	r.buckets.Current().Desired += slots
	r.lastDesired = want
	r.permitUntil = r.origin.Add(quantumEnd)
	defer func() { r.permitUntil = time.Time{} }()
	for i := uint64(0); i < slots; i++ {
		if r.intent.Load() || r.state != "running" {
			break
		}
		if !r.options.Clock.Now().Before(r.permitUntil) {
			miss := slots - i
			r.schedulerMisses += miss
			r.buckets.Current().Missed += miss
			break
		}
		sent := false
		if r.snapshot.Draft.Profile.RetryAccounting == "within" {
			if due := r.nextRetry(); due != nil {
				sent = r.dispatch(due)
			}
		}
		if !sent {
			sent = r.admit(0)
		}
		if !sent {
			miss := slots - i
			if !r.options.Clock.Now().Before(r.permitUntil) {
				r.schedulerMisses += miss
			} else {
				r.capacityMisses += miss
			}
			r.buckets.Current().Missed += miss
			break
		}
	}
}

func (r *Runner) admit(userID uint64) bool {
	if r.intent.Load() || r.state != "running" || len(r.records) >= int(r.snapshot.Draft.Capacity) {
		return false
	}
	previousSequence := r.sequence
	committed := false
	defer func() {
		if !committed {
			r.sequence = previousSequence
		}
	}()
	if r.pendingSelection == nil {
		index := r.selector.next()
		r.pendingSelection = &index
	}
	index := *r.pendingSelection
	r.sequence++
	var uid *uint64
	if userID != 0 {
		v := userID
		uid = &v
	}
	body, err := r.programs[index].Render(template.Context{Time: r.now, RunID: r.snapshot.RunID, Sequence: r.sequence, UserID: uid, Random: r.options.TokenRandom})
	if err != nil {
		r.fail("template render invariant: " + err.Error())
		return false
	}
	l := &logical{heapIndex: -1, seq: r.sequence, index: index, firstStage: r.stage, user: userID, body: body, start: r.now, deadline: r.now.Add(time.Duration(r.snapshot.Effective[index].Policy.DeadlineMS) * time.Millisecond)}
	r.records[l.seq] = l
	for _, u := range r.users {
		if u.id == userID {
			u.busy = true
		}
	}
	if !r.dispatch(l) {
		if _, ok := r.records[l.seq]; ok {
			r.finish(l, metrics.LogicalCanceled)
		}
		return false
	}
	committed = true
	r.pendingSelection = nil
	return true
}

func (r *Runner) dispatch(l *logical) bool {
	if r.intent.Load() || r.state != "running" || l.active {
		return false
	}
	now := r.options.Clock.Now()
	if !r.permitUntil.IsZero() && !now.Before(r.permitUntil) {
		return false
	}
	r.advance(now)
	if r.state != "running" || r.intent.Load() {
		return false
	}
	if !now.Before(r.origin.Add(r.total)) {
		r.drain("profile_end", false)
		return false
	}
	if l.attempt == 0 {
		l.start = now
		l.deadline = now.Add(time.Duration(r.snapshot.Effective[l.index].Policy.DeadlineMS) * time.Millisecond)
		l.firstStage = r.stage
	}
	if !now.Before(l.deadline) {
		r.finish(l, metrics.LogicalDeadline)
		return false
	}
	timeout := time.Duration(r.snapshot.Effective[l.index].Policy.TimeoutMS) * time.Millisecond
	remaining := l.deadline.Sub(now)
	deadline := remaining <= timeout
	if deadline {
		timeout = remaining
	}
	r.removeRetry(l)
	l.attempt++
	l.attemptStage = r.stage
	l.active = true
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	var uid *uint64
	if l.user != 0 {
		v := l.user
		uid = &v
	}
	a := Attempt{RunID: r.snapshot.RunID, Sequence: l.seq, Number: l.attempt, UserID: uid, Stage: r.stage, Started: now, Timeout: timeout, LogicalDeadline: deadline, Request: r.snapshot.Effective[l.index], Body: l.body, BodyCap: int(r.snapshot.Draft.Samples.BodyCapBytes)}
	l.permit = a
	if l.attempt == 1 {
		for _, agg := range r.logicalAggregates(l) {
			if err := agg.StartLogical(); err != nil {
				cancel()
				r.fail(err.Error())
				return false
			}
		}
	}
	for _, agg := range r.attemptAggregates(l) {
		if err := agg.Admit(l.attempt > 1); err != nil {
			cancel()
			r.fail(err.Error())
			return false
		}
	}
	if l.attempt > 1 {
		r.buckets.Current().Retries++
		r.templateBuckets[l.index].Current().Retries++
	} else {
		r.buckets.Current().Initial++
		r.templateBuckets[l.index].Current().Initial++
	}
	r.workers++
	r.livePermits[l.seq] = l.attempt
	go func() {
		result := r.options.Transport.DoAttempt(ctx, a)
		result.Sequence = a.Sequence
		result.Number = a.Number
		r.results <- result
	}()
	return true
}

func (r *Runner) complete(result Result) {
	if number, ok := r.livePermits[result.Sequence]; !ok || number != result.Number {
		return
	}
	delete(r.livePermits, result.Sequence)
	r.workers--
	l := r.records[result.Sequence]
	canceled := false
	if l == nil {
		l = r.canceled[result.Sequence]
		canceled = true
	}
	if l == nil {
		return
	}
	validTime := !result.Ended.IsZero() && !result.Ended.Before(l.permit.Started) && !result.Ended.After(r.now)
	if canceled {
		// Synthetic cancellation owns completion; late data enriches observations only.
		o := result.Observation
		technical := o.NetworkFailure || o.AttemptTimeout || o.DeadlineInterruption || o.BodyReadFailure
		for _, a := range r.attemptAggregates(l) {
			if o.Status >= 100 && o.Status <= 999 {
				a.Statuses[o.Status]++
			}
			if technical {
				a.TechnicalObserved++
			}
			if o.NetworkFailure {
				a.NetworkFailures++
			}
			if o.AttemptTimeout {
				a.AttemptTimeouts++
			}
			if o.DeadlineInterruption {
				a.DeadlineInterruptions++
			}
			if o.BodyReadFailure {
				a.BodyReadFailures++
			}
		}
		result.Sample.Outcome = metrics.Canceled
		r.samples.Retain(result.Sample)
		delete(r.canceled, result.Sequence)
		if !validTime {
			r.fail("invalid completion timestamp from canceled adapter")
		}
		return
	}
	l.cancel()
	l.active = false
	o := result.Observation
	if !validTime {
		o = metrics.Observation{Unknown: true}
		result.Ended = r.now
	}
	o.Duration = result.Ended.Sub(l.permit.Started)
	if validTime && result.Ended.After(l.permit.Started.Add(l.permit.Timeout)) && !o.Canceled && !o.Unknown {
		if l.permit.LogicalDeadline {
			o.DeadlineInterruption = true
		} else {
			o.AttemptTimeout = true
		}
	}
	technical := o.NetworkFailure || o.AttemptTimeout || o.DeadlineInterruption || o.BodyReadFailure
	outcome := metrics.Classify(o.Status, technical, o.Canceled, o.Unknown)
	for _, a := range r.attemptAggregates(l) {
		if err := a.Observe(o); err != nil {
			r.fail("invalid attempt observation: " + err.Error())
			return
		}
	}
	r.recordCompletion(r.snapshot.Effective[l.index].TemplateID, result.Ended, &outcome, nil, o.Duration)
	p := r.snapshot.Effective[l.index].Policy
	eligible := validTime && outcome != metrics.Success && l.attempt <= uint64(p.MaxRetries) && retryable(p, o) && result.Ended.Before(l.deadline)
	var delay time.Duration
	if eligible {
		delay = retryDelay(p, l.attempt, r.options.Random)
	}
	willRetry := eligible && r.state == "running" && !r.intent.Load() && r.now.Before(l.deadline)
	result.Sample.Outcome = outcome
	result.Sample.RetryContext = fmt.Sprintf("attempt=%d firstStage=%d attemptStage=%d retryScheduled=%t configuredDelayNs=%d", l.attempt, l.firstStage, l.attemptStage, willRetry, delay)
	r.samples.Retain(result.Sample)
	switch {
	case !validTime:
		r.finishAt(l, metrics.LogicalUnknown, r.now)
		r.fail("invalid completion timestamp from adapter")
	case outcome == metrics.Success:
		r.finishAt(l, metrics.LogicalSuccess, result.Ended)
	case o.DeadlineInterruption:
		r.finishAt(l, metrics.LogicalDeadline, minTime(result.Ended, l.deadline))
	case o.Unknown:
		r.finishAt(l, metrics.LogicalUnknown, result.Ended)
	case o.Canceled:
		r.finishAt(l, metrics.LogicalCanceled, result.Ended)
	case eligible && !r.now.Before(l.deadline):
		r.finishAt(l, metrics.LogicalDeadline, l.deadline)
	case willRetry:
		l.due = result.Ended.Add(delay)
		heap.Push(&r.retries, l)
	default:
		r.finishAt(l, metrics.LogicalFailure, result.Ended)
	}
	if r.completionPressure && r.state == "running" {
		r.fail("completion accumulator backpressure")
	}
}

func (r *Runner) stageAggregate(stage int) *metrics.Aggregate {
	a := r.stages[stage]
	if a == nil {
		a = &metrics.Aggregate{}
		r.stages[stage] = a
	}
	return a
}
func (r *Runner) attemptAggregates(l *logical) []*metrics.Aggregate {
	return []*metrics.Aggregate{&r.overall, &r.templates[l.index], r.stageAggregate(l.attemptStage)}
}
func (r *Runner) logicalAggregates(l *logical) []*metrics.Aggregate {
	return []*metrics.Aggregate{&r.overall, &r.templates[l.index], r.stageAggregate(l.firstStage)}
}
func (r *Runner) finish(l *logical, outcome metrics.LogicalOutcome) {
	at := r.now
	if outcome == metrics.LogicalDeadline {
		at = l.deadline
	}
	r.finishAt(l, outcome, at)
}
func (r *Runner) finishAt(l *logical, outcome metrics.LogicalOutcome, at time.Time) {
	if _, ok := r.records[l.seq]; !ok {
		return
	}
	r.removeRetry(l)
	delete(r.records, l.seq)
	for _, u := range r.users {
		if u.id == l.user {
			u.busy = false
		}
	}
	if l.attempt > 0 {
		duration := at.Sub(l.start)
		for _, a := range r.logicalAggregates(l) {
			if err := a.FinishLogical(outcome, duration); err != nil {
				r.runErr = err
			}
		}
		r.recordCompletion(r.snapshot.Effective[l.index].TemplateID, at, nil, &outcome, duration)
	}
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (r *Runner) scaleUsers() {
	target := int(r.snapshot.Draft.Profile.Steps[r.stage].Level)
	kept := r.users[:0]
	for _, u := range r.users {
		if !u.retiring || u.busy {
			kept = append(kept, u)
		}
	}
	r.users = kept
	active := 0
	for _, u := range r.users {
		if !u.retiring {
			active++
		}
	}
	if active > target {
		for i := len(r.users) - 1; i >= 0 && active > target; i-- {
			if !r.users[i].retiring {
				r.users[i].retiring = true
				active--
			}
		}
	}
	for _, u := range r.users {
		if active < target && u.retiring && u.busy {
			u.retiring = false
			active++
		}
	}
	for active < target && len(r.users) < int(r.snapshot.Draft.Capacity) {
		r.nextUser++
		r.users = append(r.users, &user{id: r.nextUser})
		active++
	}
}

func (r *Runner) drain(reason string, force bool) {
	if r.state == "completed" || r.state == "stopped" {
		return
	}
	if r.state == "running" || r.state == "preparing" {
		r.state = "draining"
		r.reason = reason
	}
	for _, l := range r.records {
		if !l.active {
			r.finish(l, metrics.LogicalCanceled)
			continue
		}
		if force {
			l.cancel()
			r.canceled[l.seq] = l
			o := metrics.Observation{Canceled: true, Duration: r.now.Sub(l.permit.Started)}
			for _, a := range r.attemptAggregates(l) {
				_ = a.Observe(o)
			}
			outcome := metrics.Canceled
			r.recordCompletion(r.snapshot.Effective[l.index].TemplateID, r.now, &outcome, nil, 0)
			r.finish(l, metrics.LogicalCanceled)
		}
	}
	for _, u := range r.users {
		u.retiring = true
	}
	r.captureBase()
	if force && r.forcedAt.IsZero() {
		r.forcedAt = r.now
		if r.state != "application_failed" {
			r.reason = "force_stop"
		}
	}
}

func (r *Runner) fail(reason string) {
	if r.runErr == nil {
		r.runErr = errors.New(reason)
	}
	r.state = "application_failed"
	r.reason = reason
	r.drain(reason, true)
}

func (r *Runner) demandAt(elapsed time.Duration) uint64 {
	var total uint64
	for _, s := range r.snapshot.Draft.Profile.Steps {
		if elapsed <= 0 {
			break
		}
		width := time.Duration(s.DurationSeconds) * time.Second
		part := min(width, elapsed)
		total += arrivals(s.Level, part)
		elapsed -= part
	}
	return total
}
func (r *Runner) accountSkipped(end time.Duration) {
	if r.state != "running" || r.snapshot.Draft.Profile.Mode != "open" {
		return
	}
	want := r.demandAt(min(end, r.total))
	if want > r.lastDesired {
		n := want - r.lastDesired
		r.desired += n
		r.schedulerMisses += n
		r.buckets.Current().Desired += n
		r.buckets.Current().Missed += n
		r.lastDesired = want
	}
}
