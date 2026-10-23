package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/engine"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func batch(seq uint64, index int64) engine.CompletionBatch {
	var v engine.CompletionValue
	v.Attempts[1] = 1
	if err := v.AttemptDurations[1].Add(time.Millisecond); err != nil {
		panic(err)
	}
	return engine.CompletionBatch{Version: 1, Sequence: seq, Deltas: []engine.CompletionDelta{{Index: index, Value: v}, {TemplateID: "t", Index: index, Value: v}}}
}

func TestCompletionReplayAndLateMerge(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.ElapsedNS = 2 * int64(time.Second)
	cp.CompletionBatches = []engine.CompletionBatch{batch(1, 0)}
	cp.CompletionThrough = 1
	s.fault = func(at string) error {
		if at == "after_commit" {
			return errors.New("lost acknowledgement")
		}
		return nil
	}
	if e := s.Commit(ctx, cp); e == nil {
		t.Fatal("expected lost ack")
	}
	s.fault = nil
	cp.Sequence = 2
	cp.CompletionThrough = 3
	cp.CompletionBatches = append(cp.CompletionBatches, batch(2, 0), batch(3, 4))
	cp.ElapsedNS = 4500000000
	cp.Buckets[0].Initial = 9
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	cp.Sequence = 3
	cp.CompletionBatches = nil
	cp.Buckets[0].Initial = 12
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	report, e := s.ReadReport(ctx, "r")
	if e != nil {
		t.Fatal(e)
	}
	if report.CompletionSequence != 3 || report.Buckets[0].Initial != 12 || report.Buckets[0].Errors != 2 || report.Buckets[0].Durations.Summary().Count != 2 {
		t.Fatal(report)
	}
	page, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 10})
	if e != nil || len(page.Buckets) != 2 || page.Buckets[1].Index != 4 || page.Buckets[1].EndNS != 4500000000 || page.BucketAvailability[1].BasePresent {
		t.Fatal(page, e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Recover(ctx, time.Unix(2000, 0)); e != nil {
		t.Fatal(e)
	}
	report, e = s.ReadReport(ctx, "r")
	if e != nil || report.CompletionSequence != 3 || report.LatestSequence != 3 || report.State != "interrupted" {
		t.Fatal(report, e)
	}
	t.Log("Replay skipped, late interval amended, base replacement independent, both cursors recovered")
}

func TestCompletionRollback(t *testing.T) {
	for _, mode := range []string{"after_completion", "sql_abort"} {
		t.Run(mode, func(t *testing.T) {
			s, path := openTest(t)
			ctx := context.Background()
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			cp := checkpoint(1)
			cp.CompletionBatches = []engine.CompletionBatch{batch(1, 0)}
			cp.CompletionThrough = 1
			if e := s.Commit(ctx, cp); e != nil {
				t.Fatal(e)
			}
			if mode == "sql_abort" {
				if _, e := s.writer.Exec("CREATE TRIGGER fail_completion BEFORE UPDATE OF checkpoint ON runs BEGIN SELECT RAISE(ABORT,'test abort'); END"); e != nil {
					t.Fatal(e)
				}
			} else {
				s.fault = func(at string) error {
					if at == mode {
						return errors.New("injected")
					}
					return nil
				}
			}
			cp.Sequence = 2
			cp.CompletionThrough = 2
			cp.CompletionBatches = []engine.CompletionBatch{batch(2, 0)}
			cp.Buckets[0].Initial = 99
			if e := s.Commit(ctx, cp); e == nil {
				t.Fatal("expected abort")
			}
			s.fault = nil
			s.Close()
			s, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			report, e := s.ReadReport(ctx, "r")
			if e != nil || report.LatestSequence != 1 || report.CompletionSequence != 1 || report.Buckets[0].Errors != 1 || report.Buckets[0].Initial != 1 {
				t.Fatal(report, e)
			}
			t.Log("Post-merge fault preserved both cursors and base/completion rows")
		})
	}
}

func TestCompletionValidation(t *testing.T) {
	cases := map[string]func(*engine.Checkpoint){
		"gap":     func(c *engine.Checkpoint) { c.CompletionBatches[0].Sequence = 2; c.CompletionThrough = 2 },
		"cursor":  func(c *engine.Checkpoint) { c.CompletionThrough = 2 },
		"version": func(c *engine.Checkpoint) { c.CompletionBatches[0].Version = 2 },
		"duplicate_key": func(c *engine.Checkpoint) {
			c.CompletionBatches[0].Deltas = append(c.CompletionBatches[0].Deltas, c.CompletionBatches[0].Deltas[1])
		},
		"duplicate_batch": func(c *engine.Checkpoint) { c.CompletionBatches = append(c.CompletionBatches, c.CompletionBatches[0]) },
		"scope":           func(c *engine.Checkpoint) { c.CompletionBatches[0].Deltas[1].TemplateID = "unknown" },
		"negative":        func(c *engine.Checkpoint) { c.CompletionBatches[0].Deltas[0].Index = -1 },
		"population":      func(c *engine.Checkpoint) { c.CompletionBatches[0].Deltas[0].Value.Attempts[1] = 2 },
		"unflushed":       func(c *engine.Checkpoint) { c.UnflushedCompletion = c.CompletionBatches[0].Deltas },
		"legacy_base":     func(c *engine.Checkpoint) { c.Buckets[0].Errors = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			cp := checkpoint(1)
			cp.CompletionThrough = 1
			cp.CompletionBatches = []engine.CompletionBatch{batch(1, 0)}
			mutate(&cp)
			if e := s.Commit(ctx, cp); e == nil {
				t.Fatal("invalid checkpoint accepted")
			}
			r, e := s.ReadReport(ctx, "r")
			if e != nil || r.LatestSequence != 0 || r.CompletionSequence != 0 || len(r.Completions) != 0 {
				t.Fatal(r, e)
			}
			t.Log("Invalid batch rejected atomically")
		})
	}
}

func TestCompletionOverflow(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	var v engine.CompletionValue
	v.Attempts[4] = engine.Counter(math.MaxUint64)
	cp := checkpoint(1)
	cp.CompletionThrough = 1
	cp.CompletionBatches = []engine.CompletionBatch{{Version: 1, Sequence: 1, Deltas: []engine.CompletionDelta{{Value: v}}}}
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	v.Attempts[4] = 1
	cp.Sequence = 2
	cp.CompletionThrough = 2
	cp.CompletionBatches = []engine.CompletionBatch{{Version: 1, Sequence: 2, Deltas: []engine.CompletionDelta{{Value: v}}}}
	if e := s.Commit(ctx, cp); e == nil {
		t.Fatal("overflow accepted")
	}
	r, e := s.ReadReport(ctx, "r")
	if e != nil || r.LatestSequence != 1 || r.CompletionSequence != 1 || uint64(r.Completions[0].Value.Attempts[4]) != math.MaxUint64 {
		t.Fatal(r, e)
	}
	t.Log("Checked population overflow rolls back")
}

func TestReportPages(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.ElapsedNS = 4500000000
	cp.Buckets = []engine.Bucket{{Index: 0, EndNS: int64(time.Second)}, {Index: 2, StartNS: 2 * int64(time.Second), EndNS: 3 * int64(time.Second)}}
	cp.CompletionBatches = []engine.CompletionBatch{batch(1, 1), batch(2, 4)}
	cp.CompletionThrough = 2
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	first, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 2, SampleLimit: 1})
	if e != nil || len(first.Buckets) != 2 || first.Buckets[0].Index != 0 || first.Buckets[1].Index != 1 || first.Page.TotalBuckets != 4 || first.Page.NextIndex == nil || *first.Page.NextIndex != 2 {
		t.Fatal(first, e)
	}
	revision := first.Page.Sequence
	next, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 2, FromIndex: *first.Page.NextIndex, ExpectedSequence: &revision})
	if e != nil || len(next.Buckets) != 2 || next.Buckets[1].Index != 4 || next.Page.NextIndex != nil || next.Buckets[1].EndNS != 4500000000 {
		t.Fatal(next, e)
	}
	scoped, e := s.ReadReportPage(ctx, "r", ReportQuery{TemplateID: "t", Limit: 1})
	if e != nil || scoped.Page.TotalBuckets != 2 || len(scoped.Buckets) != 1 || scoped.BucketAvailability[0].BasePresent {
		t.Fatal(scoped, e)
	}
	// Corrupt a row outside the selected page: bounded SQL must not decode it.
	if _, e = s.writer.Exec("UPDATE buckets SET data='invalid JSON' WHERE run_id='r' AND template='' AND idx=2"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1}); e != nil {
		t.Fatal("page read escaped selected keys", e)
	}
	// Restore deliberate corruption before the independent revision-pin check.
	// Base upserts now read prior CPU metadata and must fail closed on corrupt rows.
	restored, e := json.Marshal(cp.Buckets[1])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.writer.Exec("UPDATE buckets SET data=? WHERE run_id='r' AND template='' AND idx=2", restored); e != nil {
		t.Fatal(e)
	}
	cp.Sequence = 2
	cp.CompletionBatches = nil
	if e = s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1, ExpectedSequence: &revision}); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	t.Log("Union LIMIT, absent intervals, scoped totals and revision pins")
}

func TestSchemaOnePreservationAndFailureReservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE original(value TEXT); INSERT INTO original VALUES('retain'); PRAGMA user_version=1"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if _, e = Open(path); !errors.Is(e, ErrSchema) {
		t.Fatal(e)
	}
	db, e = sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var value string
	var version int
	if e = db.QueryRow("SELECT value FROM original").Scan(&value); e != nil || value != "retain" {
		t.Fatal(value, e)
	}
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version != 1 {
		t.Fatal(version, e)
	}
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e = s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.State = "application_failed"
	cp.Unfinished = []engine.Unfinished{{Sequence: 1, OutcomeCommitted: true}, {Sequence: 2}}
	if e = s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, "r"); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, fixture("other"), "other"); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	if _, e = s.Recover(ctx, time.Unix(2000, 0)); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(ctx, "r")
	if e != nil || len(r.Recovery.Unknown) != 1 || r.Recovery.Unknown[0].Sequence != 2 || len(r.Checkpoint.Unfinished) != 2 {
		t.Fatal(r, e)
	}
	t.Log("V1 preserved; failure retains reservation; committed cancellation is not unknown again")
}

func TestCompactSummaryAndScopedSamples(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.Aggregates[0].Aggregate.Started = 10
	cp.Aggregates[0].Aggregate.Attempts = []string{"5", "2", "1", "1", "1", "0"}
	cp.Samples = []engine.Sample{{Sequence: 1, TemplateID: "t"}, {Sequence: 2, TemplateID: "u"}, {Sequence: 3, TemplateID: "t"}}
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	page, e := s.ReadReportPage(ctx, "r", ReportQuery{TemplateID: "t", Limit: 1, SampleLimit: 1})
	if e != nil || page.Page.TotalSamples != 2 || len(page.Samples) != 1 || page.Samples[0].Sequence != 1 || page.Page.NextSampleOffset == nil {
		t.Fatal(page, e)
	}
	page, e = s.ReadReportPage(ctx, "r", ReportQuery{TemplateID: "t", Limit: 1, SampleLimit: 1, SampleOffset: *page.Page.NextSampleOffset})
	if e != nil || len(page.Samples) != 1 || page.Samples[0].Sequence != 3 || page.Page.NextSampleOffset != nil {
		t.Fatal(page, e)
	}
	if _, e = s.writer.Exec("UPDATE runs SET snapshot='invalid JSON',checkpoint='invalid JSON' WHERE id='r'"); e != nil {
		t.Fatal(e)
	}
	list, e := s.List(ctx, 1, 0)
	if e != nil || len(list) != 1 || list[0].Counts == nil || list[0].Counts.Attempts != 10 || list[0].Counts.Errors == nil || *list[0].Counts.Errors != 4 {
		t.Fatal(list, e)
	}
	t.Log("Scoped SQL sample pages and lightweight exact list counts")
}
