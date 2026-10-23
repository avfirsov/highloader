package report

import (
	"encoding/json"
	"highloader/internal/engine"
	"math"
	"testing"
	"time"
)

func TestOverlayRejectsMalformedCompletion(t *testing.T) {
	// checkpoint with changed base + invalid completion -> unchanged durable report
	for _, kind := range []string{"version", "gap", "invalid_population", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			h := fixture()
			h.SchemaVersion = 2
			h.LatestSequence = 1
			h.CompletionSequence = 1
			old := engine.CompletionValue{}
			old.Attempts[4] = 1
			if kind == "overflow" {
				old.Attempts[4] = engine.Counter(math.MaxUint64)
			}
			h.Completions = []engine.CompletionDelta{{Index: 0, Value: old}}
			before, _ := json.Marshal(h)
			cp := *h.Checkpoint
			cp.Sequence = 2
			cp.Cut = cp.Cut.Add(time.Second)
			cp.ElapsedNS = 2e9
			cp.Buckets = []engine.Bucket{{Index: 0, EndNS: 1e9, Initial: 999}}
			delta := engine.CompletionValue{}
			delta.Attempts[4] = 1
			batch := engine.CompletionBatch{Version: 1, Sequence: 2, Deltas: []engine.CompletionDelta{{Index: 0, Value: delta}}}
			switch kind {
			case "version":
				batch.Version = 99
			case "gap":
				batch.Sequence = 3
			case "invalid_population":
				batch.Deltas[0].Value.Attempts[0] = 1
			}
			cp.CompletionBatches = []engine.CompletionBatch{batch}
			out := Overlay(h, cp)
			after, _ := json.Marshal(out)
			source, _ := json.Marshal(h)
			if string(before) != string(after) || string(before) != string(source) {
				t.Fatal("rejected overlay partially mutated durable evidence")
			}
			if out.Checkpoint.Sequence != 1 || !out.DurableCut.Equal(*h.DurableCut) {
				t.Fatal("durable provenance lost")
			}
			t.Log("malformed completion rejected atomically; durable input and cut retained")
		})
	}
}

func TestCompletionOnlyAvailability(t *testing.T) {
	// absent base + measured completion -> explicit missing dispatch evidence
	for _, elapsed := range []int64{0, 500000000} {
		t.Run(time.Duration(elapsed).String(), func(t *testing.T) {
			h := fixture()
			h.SchemaVersion = 2
			h.LatestSequence = 1
			h.Buckets = nil
			cp := *h.Checkpoint
			cp.Sequence = 2
			cp.ElapsedNS = elapsed
			v := engine.CompletionValue{}
			v.Attempts[0] = 1
			v.AttemptDurations[0].Add(time.Millisecond)
			cp.CompletionBatches = []engine.CompletionBatch{{Version: 1, Sequence: 1, Deltas: []engine.CompletionDelta{{Index: 0, Value: v}}}}
			out := Overlay(h, cp)
			r, err := Build(out, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Points) != 1 {
				t.Fatalf("missing completion point: %+v", r.Points)
			}
			p := r.Points[0]
			if p.BasePresent || p.InitialRate != nil || p.RetryRate != nil || p.TotalRate != nil || p.DesiredRate != nil || p.TargetUsers != nil || p.ActiveUsers != nil || p.RetiringUsers != nil {
				t.Fatalf("invented base evidence: %+v", p)
			}
			if p.CompletedCount != "1" {
				t.Fatal("lost measured completion")
			}
			if elapsed == 0 {
				if p.IntervalKnown || p.CompletedRate != nil {
					t.Fatal("invented interval")
				}
			} else {
				if !p.IntervalKnown || p.CompletedRate == nil || *p.CompletedRate != 2 {
					t.Fatal("lost known partial interval rate")
				}
			}
			if len(h.Buckets) != 0 || len(h.Completions) != 0 {
				t.Fatal("mutated durable source")
			}
			t.Log("completion retained; dispatch/users absent; rate uses only known interval")
		})
	}
}
