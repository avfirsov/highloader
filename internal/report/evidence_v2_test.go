package report

import (
	"highloader/internal/engine"
	"highloader/internal/history"
	"testing"
	"time"
)

func TestSchema2Projection(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.LatestSequence = 1
	h.Checkpoint.ElapsedNS = 2e9
	h.Checkpoint.CPUStats = engine.CPUSummary{Valid: 1, Missing: 2, Sum: 25, Min: ptr(25), Max: ptr(25)}
	h.Snapshot.Draft.Profile.Mode = "closed"
	users := int64(10)
	h.Buckets[0].TargetUsers = &users
	h.Aggregates[0].Aggregate.TechnicalObserved = 7
	v := engine.CompletionValue{}
	v.Attempts[0] = 1
	v.AttemptDurations[0].Add(time.Millisecond)
	v.Attempts[1] = 1
	v.AttemptDurations[1].Add(100 * time.Millisecond)
	v.Attempts[4] = 1
	h.Completions = []engine.CompletionDelta{{Index: 0, Value: v}}
	h.BucketAvailability = []history.BucketAvailability{{Index: 0, BasePresent: true, IntervalKnown: true}, {Index: 1, BasePresent: false, IntervalKnown: false}}
	h.Page = &history.ReportPage{FromIndex: 0, TotalBuckets: 900, TotalSamples: 500, Sequence: 1, SampleOffset: 200}
	h.Samples = []engine.Sample{{Sequence: 201}}
	h.Recovery = &history.Recovery{Unknown: []engine.Unfinished{{OutcomeCommitted: true, Active: true}}}
	r, e := Build(h, Options{Population: "http", Limit: 2, SampleOffset: 200, SampleLimit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r.Points[0].P50MS == nil || *r.Points[0].P50MS < 100 || r.Points[0].CompletedCount != "3" || r.Points[0].MeasuredFinished != "2" || r.Points[0].ErrorCount != "1" {
		t.Fatalf("completion populations: %+v", r.Points[0])
	}
	if r.Points[0].TargetUsers == nil || *r.Points[0].TargetUsers != "10" || r.Points[1].InitialRate != nil || r.Points[1].CompletedRate != nil {
		t.Fatal("availability invented")
	}
	if r.Summary.CPU.Count != "1" || r.Summary.CPU.Missing != "2" || *r.Summary.CPU.MeanPercent != 25 || r.Summary.TechnicalErrors != "7" || r.Summary.UnknownAttempts != "0" {
		t.Fatalf("summary %+v", r.Summary)
	}
	if len(r.Samples) != 1 || r.Range.TotalBuckets != "900" || r.Range.TotalSamples != "500" {
		t.Fatal("double page")
	}
	if *r.Summary.MeasurementSeconds != 2 || *r.Summary.TotalRate != 2 {
		t.Fatal("global denominator")
	}
	t.Log("completion populations, canceled tombstones, global CPU and SQL page totals verified")
}

func TestSchema2Overlay(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.CompletionSequence = 1
	h.LatestSequence = 1
	v := engine.CompletionValue{}
	v.Attempts[0] = 1
	v.AttemptDurations[0].Add(time.Millisecond)
	h.Completions = []engine.CompletionDelta{{Index: 0, Value: v}}
	cp := *h.Checkpoint
	cp.Sequence = 2
	cp.Cut = cp.Cut.Add(time.Second)
	cp.ElapsedNS = 2e9
	cp.CompletionThrough = 2
	cp.CompletionBatches = []engine.CompletionBatch{{Version: 1, Sequence: 1, Deltas: []engine.CompletionDelta{{Index: 0, Value: v}}}, {Version: 1, Sequence: 2, Deltas: []engine.CompletionDelta{{Index: 0, Value: v}}}}
	cp.UnflushedCompletion = []engine.CompletionDelta{{Index: 0, Value: v}}
	out := Overlay(h, cp)
	r, e := Build(out, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if r.Points[0].CompletedCount != "3" || r.Revision != "2" || r.DurableRevision != "1" || r.Durable {
		t.Fatalf("replay %+v", r)
	}
	again := Overlay(out, cp)
	if again.Completions[0].Value.Attempts[0] != 3 {
		t.Fatal("same revision replay")
	}
	if h.Completions[0].Value.Attempts[0] != 1 {
		t.Fatal("mutated durable")
	}
}

func TestEqualCutRevisionDurability(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.LatestSequence = h.Checkpoint.Sequence
	durable, err := Build(h, Options{})
	if err != nil || !durable.Durable {
		t.Fatalf("acknowledged evidence: %+v, %v", durable, err)
	}
	cp := *h.Checkpoint
	cp.Sequence++
	r, err := Build(Overlay(h, cp), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Durable || r.Revision != "2" || r.DurableRevision != "1" || r.DurableCut == nil || !r.DurableCut.Equal(*h.DurableCut) || !r.Cut.Equal(cp.Cut) {
		t.Fatalf("equal-cut unsaved provenance: %+v", r)
	}
	t.Log("equal-cut newer revision remains unsaved; acknowledged revision and cut retained")
}

func TestOverlayCPUAmendments(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.LatestSequence = 1
	users := int64(3)
	h.Buckets[0].TargetUsers = &users
	h.Buckets[0].CPUAt = h.Checkpoint.Cut.Add(-time.Second)
	h.Buckets = append(h.Buckets, engine.Bucket{Index: 5})
	h.BucketAvailability = []history.BucketAvailability{{Index: 0, BasePresent: true}, {Index: 5, BasePresent: false}}
	cp := *h.Checkpoint
	cp.Sequence = 2
	cp.CPUAmendments = []engine.CPUAmendment{{Index: 0, At: cp.Cut, Identity: "late", Percent: ptr(42)}, {Index: 5, At: cp.Cut, Identity: "missing"}, {Index: 9, At: cp.Cut, Identity: "outside"}}
	out := Overlay(h, cp)
	b := out.Buckets[0]
	if b.CPU == nil || *b.CPU != 42 || b.CPUIdentity != "late" || b.Initial != 2 || *b.TargetUsers != 3 || len(out.Buckets) != len(h.Buckets) || out.Buckets[2].CPU != nil || out.Buckets[3].CPUIdentity != "" || h.Buckets[0].CPU != nil {
		t.Fatalf("amendment corrupted base or provenance: %+v", out.Buckets)
	}
	cp.Sequence = 3
	cp.CPUAmendments = nil
	cp.Buckets = []engine.Bucket{h.Buckets[0]}
	cp.Buckets[0].Initial = 4
	out = Overlay(out, cp)
	if out.Buckets[0].Initial != 4 || out.Buckets[0].CPUIdentity != "late" || *out.Buckets[0].CPU != 42 {
		t.Fatal("stale base erased later CPU")
	}
	r, err := Build(out, Options{})
	if err != nil || r.Points[0].CPU == nil || *r.Points[0].CPU != 42 {
		t.Fatalf("available report lost late CPU: %+v %v", r.Points, err)
	}
	t.Log("late CPU retained through stale base; dispatch/users and absent/template bases preserved")
}

func TestDormantAuth(t *testing.T) {
	r, _ := Build(fixture(), Options{})
	r.Snapshot.Draft.Auth.Mode = "none"
	r.Snapshot.Draft.Auth.Password = "DORMANT-SECRET-724"
	r.Snapshot.Draft.URL = "https://example.invalid/DORMANT-SECRET-724"
	b, e := Export(r, false, []byte("test"), []byte("test"))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i+len("DORMANT-SECRET-724") <= len(b); i++ {
		if string(b[i:i+len("DORMANT-SECRET-724")]) == "DORMANT-SECRET-724" {
			t.Fatal("dormant leak")
		}
	}
}

func TestBoundedUnsavedTail(t *testing.T) {
	h := fixture()
	h.SchemaVersion = 2
	h.Buckets = nil
	h.Aggregates = nil
	h.Checkpoint = nil
	h.Page = &history.ReportPage{Scope: "", Limit: 2, TotalBuckets: 0}
	cp := engine.Checkpoint{RunID: h.RunID, Sequence: 1, Cut: time.Now(), ElapsedNS: 3e9, Buckets: []engine.Bucket{{Index: 0, EndNS: 1e9, Initial: 1}, {Index: 1, StartNS: 1e9, EndNS: 2e9, Initial: 2}, {Index: 2, StartNS: 2e9, EndNS: 3e9, Initial: 3}}}
	out := Overlay(h, cp)
	r, e := Build(out, Options{Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Points) != 2 || r.Range.TotalBuckets != "0" || r.Range.VolatileAdditionalKeys != "2" || *r.Points[0].InitialRate != 1 {
		t.Fatalf("tail %+v", r)
	}
	full := fixture()
	full.SchemaVersion = 2
	next := int64(2)
	full.Page = &history.ReportPage{Limit: 2, NextIndex: &next, TotalBuckets: 3}
	cp.Sequence = 2
	cp.Cut = full.Checkpoint.Cut.Add(time.Second)
	before := len(full.Buckets)
	out = Overlay(full, cp)
	if len(out.Buckets) != before || out.VolatileAdditionalKeys != 0 {
		t.Fatal("outside page appended")
	}
}
