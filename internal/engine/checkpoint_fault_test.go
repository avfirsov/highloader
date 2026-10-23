package engine

import (
	"context"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestPendingCheckpointStageRetention(t *testing.T) {
	// stage0 workers -> stage1 -> live ack -> force tombstones -> late enrichment -> final ack
	s := fixture()
	s.Draft.Profile.Mode = "closed"
	s.Draft.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 3}, {DurationSeconds: 10, Level: 0}}
	s.Effective[0].Policy.TimeoutMS = 5000
	s.Effective[0].Policy.DeadlineMS = 10000
	r, clock, _ := controlled(t, s)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	calls := make(chan Attempt, 3)
	r.options.Transport = transportFunc(func(ctx context.Context, a Attempt) Result {
		calls <- a
		<-releases[a.Sequence-1]
		return Result{Ended: clock.Now(), Observation: metrics.Observation{Status: 200}}
	})
	type heldCommit struct {
		cut     Checkpoint
		release chan struct{}
	}
	entered := make(chan heldCommit, 8)
	var active, overlap atomic.Int32
	r.options.Sink = sinkFunc(func(ctx context.Context, c Checkpoint) error {
		if active.Add(1) != 1 {
			overlap.Add(1)
		}
		defer active.Add(-1)
		gate := make(chan struct{})
		entered <- heldCommit{c, gate}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	t.Cleanup(func() {
		for _, ch := range releases {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	})
	receive := func() heldCommit {
		t.Helper()
		select {
		case v := <-entered:
			return v
		case <-time.After(3 * time.Second):
			t.Fatal("commit entry missing")
			return heldCommit{}
		}
	}
	ack := func(h heldCommit) {
		t.Helper()
		close(h.release)
		select {
		case a := <-r.acks:
			r.acknowledge(a)
		case <-time.After(3 * time.Second):
			t.Fatal("commit return missing")
		}
	}
	complete := func(index int) {
		t.Helper()
		close(releases[index])
		select {
		case result := <-r.results:
			r.complete(result)
		case <-time.After(3 * time.Second):
			t.Fatal("worker return missing")
		}
	}
	r.schedule()
	for range 3 {
		<-calls
	}
	r.queueCheckpoint(false)
	first := receive()
	clock.set(time.Second)
	r.advance(clock.Now())
	if r.stage != 1 || r.pending == nil {
		t.Fatal("real stage boundary did not queue evidence")
	}
	skipped := r.pending.Sequence
	complete(0)
	r.queueCheckpoint(false)
	newest := clone(*r.pending)
	if newest.Sequence <= skipped || newest.CompletionThrough == 0 {
		t.Fatal("new completion did not replace pending cut")
	}
	select {
	case <-entered:
		t.Fatal("second Commit entered while first held")
	default:
	}
	ack(first)
	second := receive()
	if !reflect.DeepEqual(clone(second.cut), newest) || r.stages[0] == nil || len(r.records) != 2 {
		t.Fatal("newest pending/live old stage lost")
	}
	r.drain("test force", true)
	r.queueCheckpoint(false)
	replaced := r.pending.Sequence
	clock.set(1100 * time.Millisecond)
	r.advance(clock.Now())
	r.queueCheckpoint(false)
	newest = clone(*r.pending)
	if newest.Sequence <= replaced || len(r.canceled) != 2 || r.workers != 2 {
		t.Fatal("force did not retain actual workers")
	}
	ack(second)
	third := receive()
	if !reflect.DeepEqual(clone(third.cut), newest) || r.stages[0] == nil {
		t.Fatal("canceled old stage or cumulative pending lost")
	}
	complete(1)
	complete(2)
	if r.workers != 0 || len(r.canceled) != 0 || len(r.records) != 0 {
		t.Fatal("workers not actually released")
	}
	r.queueCheckpoint(false)
	final := clone(*r.pending)
	ack(third)
	fourth := receive()
	if r.stages[0] == nil {
		t.Fatal("old acknowledgment evicted stage enriched after its cut")
	}
	if !reflect.DeepEqual(clone(fourth.cut), final) {
		t.Fatal("final pending cut changed")
	}
	var totals CompletionValue
	seen := map[uint64]CompletionBatch{}
	for _, cut := range []Checkpoint{first.cut, second.cut, third.cut, fourth.cut} {
		for _, batch := range cut.CompletionBatches {
			if old, ok := seen[batch.Sequence]; ok {
				if !reflect.DeepEqual(old, batch) {
					t.Fatal("replayed batch changed")
				}
				continue
			}
			seen[batch.Sequence] = batch
			for _, delta := range batch.Deltas {
				if delta.TemplateID == "" {
					if err := totals.Merge(delta.Value); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	if totals.Attempts[metrics.Success] != 1 || totals.Attempts[metrics.Canceled] != 2 || totals.Logical[metrics.LogicalSuccess] != 1 || totals.Logical[metrics.LogicalCanceled] != 2 {
		t.Fatal("coalescing lost or fabricated unique completions", totals)
	}
	if r.overall.Initial != 3 || r.overall.Statuses[200] != 3 || r.overall.Logical[metrics.LogicalSuccess] != 1 || r.overall.Logical[metrics.LogicalCanceled] != 2 {
		t.Fatal("late observations changed outcome ownership", r.overall)
	}
	ack(fourth)
	if r.stages[0] != nil {
		t.Fatal("fully acknowledged unused stage retained")
	}
	// Reproduce Run's terminal transition only after workers AND writer have returned.
	r.state = "stopped"
	r.sealFinal()
	r.queueCheckpoint(true)
	r.finalQueued = true
	fifth := receive()
	final = fifth.cut
	ack(fifth)
	r.publish()
	if r.stages[0] != nil || r.pending != nil || r.writing || active.Load() != 0 || overlap.Load() != 0 || !r.View().Durable || !r.View().WorkersReleased || !r.View().WriterReleased || r.durableSeq != final.Sequence || r.durableCut != final.Cut {
		t.Fatal("final evidence or ownership mismatch", r.View())
	}
	t.Log("newest cumulative cuts retained all three completions; live/canceled/changed old stage survived old acknowledgments; five serialized commits and all workers released")
}
