package engine

import (
	"context"
	"fmt"
	"highloader/internal/metrics"
	"math"
	"reflect"
	"sort"
	"strconv"
	"time"
)

func aggregateWire(a *metrics.Aggregate) Aggregate {
	v := Aggregate{Initial: a.Initial, Retries: a.Retries, Started: a.Started, Statuses: map[string]string{}, NetworkFailures: a.NetworkFailures, AttemptTimeouts: a.AttemptTimeouts, DeadlineInterruptions: a.DeadlineInterruptions, BodyReadFailures: a.BodyReadFailures}
	v.TechnicalObserved = a.TechnicalObserved
	for i, n := range a.Statuses {
		if n != 0 {
			v.Statuses[strconv.Itoa(i)] = strconv.FormatUint(n, 10)
		}
	}
	for _, n := range a.Attempts {
		v.Attempts = append(v.Attempts, strconv.FormatUint(n, 10))
	}
	for _, n := range a.Logical {
		v.Logical = append(v.Logical, strconv.FormatUint(n, 10))
	}
	for _, d := range a.AttemptDurations {
		v.AttemptDurations = append(v.AttemptDurations, clone(d))
	}
	for _, d := range a.LogicalDurations {
		v.LogicalDurations = append(v.LogicalDurations, clone(d))
	}
	return v
}

func (r *Runner) checkpoint(final bool) Checkpoint {
	r.checkpointSeq++
	c := Checkpoint{RunID: r.snapshot.RunID, Sequence: r.checkpointSeq, Cut: r.now, State: r.state, Reason: r.reason, Final: final, Desired: r.desired, SchedulerMisses: r.schedulerMisses, CapacityMisses: r.capacityMisses}
	c.StartedAt = r.origin
	c.ElapsedNS = int64(r.elapsed)
	c.CompletionBatches, c.CompletionThrough = r.completionSnapshot()
	c.UnflushedCompletion = r.unflushedCompletion()
	c.CPUStats = clone(r.cpuStats)
	for _, amendment := range r.cpuAmendments {
		c.CPUAmendments = append(c.CPUAmendments, clone(amendment))
	}
	sort.Slice(c.CPUAmendments, func(i, j int) bool { return c.CPUAmendments[i].Index < c.CPUAmendments[j].Index })
	for _, sample := range r.samples.All() {
		c.Samples = append(c.Samples, Sample(sample))
	}
	c.Aggregates = append(c.Aggregates, Dimension{Kind: "overall", Stage: -1, Aggregate: aggregateWire(&r.overall)})
	for i := range r.templates {
		c.Aggregates = append(c.Aggregates, Dimension{Kind: "template", TemplateID: r.snapshot.Effective[i].TemplateID, Stage: -1, Aggregate: aggregateWire(&r.templates[i])})
	}
	for stage, a := range r.stages {
		c.Aggregates = append(c.Aggregates, Dimension{Kind: "stage", Stage: stage, Aggregate: aggregateWire(a)})
	}
	buckets := r.buckets.Checkpoint()
	if r.emergencyBucket != nil {
		buckets = append(buckets, *r.emergencyBucket)
	}
	for _, b := range buckets {
		c.Buckets = append(c.Buckets, r.baseBucket(b, ""))
	}
	for i, scope := range r.templateBuckets {
		values := scope.Checkpoint()
		if r.emergencyBucket != nil {
			current := clone(*scope.Current())
			current.End = r.emergencyBucket.End
			values = append(values, current)
		}
		for _, b := range values {
			c.Buckets = append(c.Buckets, r.baseBucket(b, r.snapshot.Effective[i].TemplateID))
		}
	}
	for _, l := range r.records {
		var uid *uint64
		if l.user != 0 {
			v := l.user
			uid = &v
		}
		c.Unfinished = append(c.Unfinished, Unfinished{Sequence: l.seq, Attempt: l.attempt, TemplateID: r.snapshot.Effective[l.index].TemplateID, UserID: uid, FirstStage: l.firstStage, AttemptStage: l.attemptStage, Active: l.active})
	}
	for _, l := range r.canceled {
		c.Unfinished = append(c.Unfinished, Unfinished{Sequence: l.seq, Attempt: l.attempt, TemplateID: r.snapshot.Effective[l.index].TemplateID, FirstStage: l.firstStage, AttemptStage: l.attemptStage, Active: true, OutcomeCommitted: true})
	}
	return c
}

func (r *Runner) queueCheckpoint(final bool) {
	r.flushCompletion(final)
	c := r.checkpoint(final)
	r.mu.Lock()
	r.report = c
	r.mu.Unlock()
	if r.persistErr != "" || len(c.UnflushedCompletion) > 0 {
		return
	}
	if r.writing {
		r.pending = &c
		return
	}
	r.write(c)
}

func (r *Runner) write(c Checkpoint) {
	r.writing = true
	r.activeWriteSeq = c.Sequence
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.options.CommitTimeout)
		defer cancel()
		returned := make(chan struct{})
		watchdogDone := make(chan struct{})
		go func() {
			defer close(watchdogDone)
			select {
			case <-ctx.Done():
				r.acks <- acknowledgement{checkpoint: c, err: ctx.Err(), timedOut: true}
			case <-returned:
			}
		}()
		var err error
		if r.options.Sink != nil {
			err = r.options.Sink.Commit(ctx, c)
		}
		close(returned)
		<-watchdogDone
		r.acks <- acknowledgement{checkpoint: c, err: err}
	}()
}

func (r *Runner) acknowledge(ack acknowledgement) {
	if ack.timedOut {
		if r.writing && ack.checkpoint.Sequence == r.activeWriteSeq {
			r.writerTimedOut = true
			r.persistErr = ack.err.Error()
			r.pending = nil
			r.fail("persistence watchdog: " + ack.err.Error())
			r.publish()
		}
		return
	}
	r.writing = false
	if ack.err != nil {
		r.persistErr = ack.err.Error()
		r.pending = nil
		r.fail("persistence failure: " + ack.err.Error())
		if r.finalQueued {
			r.queueCheckpoint(true)
		}
		return
	}
	r.durableSeq = ack.checkpoint.Sequence
	r.durableCut = ack.checkpoint.Cut
	for _, amendment := range ack.checkpoint.CPUAmendments {
		if current, ok := r.cpuAmendments[amendment.Index]; ok && current.At.Equal(amendment.At) && current.Identity == amendment.Identity {
			delete(r.cpuAmendments, amendment.Index)
		}
	}
	r.completionAck = ack.checkpoint.CompletionThrough
	kept := r.completionBatches[:0]
	for _, batch := range r.completionBatches {
		if batch.Sequence > r.completionAck {
			kept = append(kept, batch)
		}
	}
	for i := len(kept); i < len(r.completionBatches); i++ {
		r.completionBatches[i] = CompletionBatch{}
	}
	r.completionBatches = kept
	for _, b := range ack.checkpoint.Buckets {
		if b.TemplateID == "" {
			_ = r.buckets.Ack(b.Index)
			delete(r.baseMeta, b.Index)
		} else {
			for i, e := range r.snapshot.Effective {
				if e.TemplateID == b.TemplateID {
					_ = r.templateBuckets[i].Ack(b.Index)
				}
			}
		}
	}
	for _, dimension := range ack.checkpoint.Aggregates {
		if dimension.Kind != "stage" || dimension.Stage >= r.stage {
			continue
		}
		used := false
		for _, l := range r.records {
			if l.firstStage == dimension.Stage || l.active && l.attemptStage == dimension.Stage {
				used = true
				break
			}
		}
		for _, l := range r.canceled {
			if l.firstStage == dimension.Stage || l.attemptStage == dimension.Stage {
				used = true
			}
		}
		// an older acknowledgement cannot evict a stage changed after its cut.
		current := r.stages[dimension.Stage]
		if !used && current != nil && reflect.DeepEqual(aggregateWire(current), dimension.Aggregate) {
			delete(r.stages, dimension.Stage)
		}
	}
	if r.pending != nil && r.persistErr == "" {
		next := *r.pending
		r.pending = nil
		r.write(next)
	}
}

func (r *Runner) sealFinal() {
	end := r.elapsed
	if end-r.buckets.Current().Start > time.Second {
		end = r.buckets.Current().Start + time.Second
	}
	if err := r.buckets.Seal(end); err != nil {
		current := clone(*r.buckets.Current())
		current.End = end
		r.emergencyBucket = &current
		r.persistErr = fmt.Sprintf("final bucket: %v", err)
		r.fail(r.persistErr)
		return
	}
	for _, b := range r.templateBuckets {
		if err := b.Seal(end); err != nil {
			r.fail("final template bucket: " + err.Error())
		}
	}
}

func (r *Runner) publish() {
	// Published points retain their frozen profile even after preparation unlocks.
	v := View{Mode: r.snapshot.Draft.Profile.Mode, RetryAccounting: r.snapshot.Draft.Profile.RetryAccounting, RunID: r.snapshot.RunID, State: r.state, Reason: r.reason, ElapsedMS: r.elapsed.Milliseconds(), RemainingMS: max(int64(0), (r.total - r.elapsed).Milliseconds()), Stage: r.stage, Aggregate: aggregateWire(&r.overall), Desired: r.desired, SchedulerMisses: r.schedulerMisses, CapacityMisses: r.capacityMisses, Unfinished: len(r.records), Capacity: int(r.snapshot.Draft.Capacity), DurableSequence: r.durableSeq, DurableCut: r.durableCut, PersistenceError: r.persistErr, WorkersReleased: r.workers == 0, Points: r.livePoints(), CPUReason: "CPU source unavailable"}
	for _, u := range r.users {
		if u.retiring && u.busy {
			v.RetiringUsers++
		} else if !u.retiring {
			v.ActiveUsers++
		}
	}
	r.captureBase()
	meta := r.baseMeta[r.buckets.Current().Index]
	v.CPU = meta.CPU
	v.CPUReason = meta.CPUReason
	v.CPUStats = clone(r.cpuStats)
	v.TargetUsers = meta.TargetUsers
	v.WriterReleased = !r.writing
	if n := len(v.Points); n > 0 {
		p := v.Points[n-1]
		v.CompletedRate = p.CompletedRate
		width := (p.End - p.Start).Seconds()
		if width > 0 {
			v.InitialRate = float64(p.Initial) / width
			v.RetryRate = float64(p.Retries) / width
		}
	}
	v.TotalRate = v.InitialRate + v.RetryRate
	if r.snapshot.Draft.Profile.Mode == "open" {
		desired := 0.0
		if r.state == "running" {
			desired = float64(r.snapshot.Draft.Profile.Steps[r.stage].Level)
		}
		v.DesiredRate = &desired
		actual := v.TotalRate
		if r.snapshot.Draft.Profile.RetryAccounting == "above" {
			actual = v.InitialRate
		}
		short := math.Max(0, desired-actual)
		v.Shortfall = &short
	}
	v.Durable = r.finalQueued && r.persistErr == "" && !r.writing && r.pending == nil && r.durableSeq == r.checkpointSeq
	r.mu.Lock()
	v.Revision = r.view.Revision + 1
	r.view = v
	r.mu.Unlock()
}
