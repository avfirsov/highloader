package history

import (
	"context"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"reflect"
	"testing"
	"time"
)

func TestStoredReadFailures(t *testing.T) {
	for _, c := range []struct {
		name, sql string
		read      func(context.Context, *Store) error
	}{
		{"snapshot", `UPDATE runs SET snapshot='{'`, func(ctx context.Context, s *Store) error { _, e := s.Get(ctx, "r"); return e }},
		{"checkpoint", `UPDATE runs SET checkpoint='{'`, func(ctx context.Context, s *Store) error { _, e := s.Get(ctx, "r"); return e }},
		{"recovery", `UPDATE runs SET recovery='{'`, func(ctx context.Context, s *Store) error { _, e := s.Get(ctx, "r"); return e }},
		{"counts_record", `UPDATE runs SET counts='{'`, func(ctx context.Context, s *Store) error { _, e := s.Get(ctx, "r"); return e }},
		{"counts_list", `UPDATE runs SET counts='{'`, func(ctx context.Context, s *Store) error { _, e := s.List(ctx, 10, 0); return e }},
		{"cut_list", `UPDATE runs SET cut='not-a-time'`, func(ctx context.Context, s *Store) error { _, e := s.List(ctx, 10, 0); return e }},
		{"bucket", `UPDATE buckets SET data='{'`, func(ctx context.Context, s *Store) error { _, e := s.ReadReport(ctx, "r"); return e }},
		{"aggregate", `UPDATE aggregates SET data='{'`, func(ctx context.Context, s *Store) error { _, e := s.ReadReport(ctx, "r"); return e }},
		{"sample", `UPDATE samples SET data='{'`, func(ctx context.Context, s *Store) error { _, e := s.ReadReport(ctx, "r"); return e }},
		{"page_aggregate", `UPDATE aggregates SET data='{'`, func(ctx context.Context, s *Store) error {
			_, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1, SampleLimit: 1})
			return e
		}},
		{"page_sample", `UPDATE samples SET data='{'`, func(ctx context.Context, s *Store) error {
			_, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1, SampleLimit: 1})
			return e
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := openTest(t)
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			if e := s.Commit(ctx, checkpoint(1)); e != nil {
				t.Fatal(e)
			}
			if _, e := s.writer.Exec(c.sql); e != nil {
				t.Fatal(e)
			}
			if e := c.read(ctx, s); e == nil {
				t.Fatal("corrupt evidence accepted")
			}
			var seq string
			if e := s.writer.QueryRow("SELECT seq FROM runs WHERE id='r'").Scan(&seq); e != nil || seq != "1" {
				t.Fatal(seq, e)
			}
			t.Log("corruption rejected without changing durable revision")
		})
	}
}

func TestEvidenceReaderPages(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.Samples = []engine.Sample{{RunID: "r", Sequence: 9007199254740993, Attempt: 1}, {RunID: "r", Sequence: 2, Attempt: 2}, {RunID: "r", Sequence: 2, Attempt: 1}}
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	first, e := s.Samples(ctx, "r", 2, 0)
	if e != nil || len(first) != 2 || first[0].Sequence != 2 || first[0].Attempt != 1 || first[1].Attempt != 2 {
		t.Fatal(first, e)
	}
	second, e := s.Samples(ctx, "r", 2, 2)
	if e != nil || len(second) != 1 || second[0].Sequence != 9007199254740993 {
		t.Fatal(second, e)
	}
	empty, e := s.Samples(ctx, "r", 2, 3)
	if e != nil || empty == nil || len(empty) != 0 {
		t.Fatal(empty, e)
	}
	a, e := s.Aggregates(ctx, "r")
	if e != nil || !reflect.DeepEqual(a, cp.Aggregates) {
		t.Fatal(a, e)
	}
	a[0].Aggregate.Initial = 99
	again, e := s.Aggregates(ctx, "r")
	if e != nil || again[0].Aggregate.Initial != 1 {
		t.Fatal(again, e)
	}
	t.Log("numeric sample ordering, disjoint pages and detached aggregates")
}

func TestReaderRangeAndClosedErrors(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	for _, p := range [][2]int{{0, 0}, {1001, 0}, {1, -1}} {
		if _, e := s.List(ctx, p[0], p[1]); e == nil {
			t.Fatal("list", p)
		}
		if _, e := s.Samples(ctx, "r", p[0], p[1]); e == nil {
			t.Fatal("samples", p)
		}
	}
	for _, q := range []ReportQuery{{Limit: 0}, {Limit: 2001}, {Limit: 1, FromIndex: -1}, {Limit: 1, SampleOffset: -1}, {Limit: 1, SampleLimit: -1}, {Limit: 1, SampleLimit: 2001}} {
		if _, e := s.ReadReportPage(ctx, "r", q); e == nil {
			t.Fatal(q)
		}
	}
	if _, e := s.BucketsForTemplate(ctx, "r", "", -1, 1); e == nil {
		t.Fatal("negative bucket accepted")
	}
	if _, e := s.ReadReport(ctx, "missing"); e != ErrNotFound {
		t.Fatal(e)
	}
	if _, e := s.ReadReportPage(ctx, "missing", ReportQuery{Limit: 1}); e != ErrNotFound {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	for _, read := range []func() error{
		func() error { _, e := s.List(ctx, 1, 0); return e }, func() error { _, e := s.Get(ctx, "r"); return e },
		func() error { _, e := s.Samples(ctx, "r", 1, 0); return e }, func() error { _, e := s.Aggregates(ctx, "r"); return e },
		func() error { _, e := s.ReadReport(ctx, "r"); return e }, func() error { _, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1}); return e },
	} {
		if e := read(); e == nil {
			t.Fatal("closed store read succeeded")
		}
	}
	t.Log("invalid ranges and unavailable reads cannot masquerade as empty evidence")
}

func TestRejectedWritesPreserveEvidence(t *testing.T) {
	for _, c := range []struct {
		name string
		edit func(*engine.Checkpoint)
	}{
		{"zero sequence", func(c *engine.Checkpoint) { c.Sequence = 0 }},
		{"zero cut", func(c *engine.Checkpoint) { c.Cut = time.Time{} }},
		{"unknown state", func(c *engine.Checkpoint) { c.State = "invented" }},
		{"terminal not final", func(c *engine.Checkpoint) { c.State = "completed" }},
		{"unfinished final", func(c *engine.Checkpoint) { c.State = "completed"; c.Final = true }},
		{"backwards cut", func(c *engine.Checkpoint) { c.Cut = checkpoint(1).Cut.Add(-time.Second) }},
		{"foreign sample", func(c *engine.Checkpoint) { c.Samples[0].RunID = "other" }},
		{"negative bucket", func(c *engine.Checkpoint) { c.Buckets[0].Index = -1 }},
		{"legacy bucket errors", func(c *engine.Checkpoint) { c.Buckets[0].Errors = 1 }},
		{"invalid summary", func(c *engine.Checkpoint) { c.Aggregates[0].Aggregate.Attempts = []string{"0", "bad", "0", "0"} }},
		{"overflow summary", func(c *engine.Checkpoint) {
			c.Aggregates[0].Aggregate.Attempts = []string{"0", "18446744073709551615", "1", "0"}
		}},
		{"too many CPU amendments", func(c *engine.Checkpoint) { c.CPUAmendments = make([]engine.CPUAmendment, 13) }},
		{"invalid CPU amendment", func(c *engine.Checkpoint) { c.CPUAmendments = []engine.CPUAmendment{{Index: 0}} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := openTest(t)
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			if e := s.Commit(ctx, checkpoint(1)); e != nil {
				t.Fatal(e)
			}
			before, e := s.ReadReport(ctx, "r")
			if e != nil {
				t.Fatal(e)
			}
			cp := checkpoint(2)
			c.edit(&cp)
			if e := s.Commit(ctx, cp); e == nil {
				t.Fatal("invalid write accepted")
			}
			after, e := s.ReadReport(ctx, "r")
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected write changed evidence", e)
			}
			t.Log("rejected checkpoint left prior complete report unchanged")
		})
	}
}

func TestInvalidStartsLeaveNoRows(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	for _, edit := range []func(*domain.RunSnapshot){
		func(s *domain.RunSnapshot) { s.RunID = "" }, func(s *domain.RunSnapshot) { s.SchemaVersion = 99 },
		func(s *domain.RunSnapshot) { s.CreatedAt = "invalid" }, func(s *domain.RunSnapshot) { s.Draft.Templates[0].Body = json.RawMessage(`{`) },
	} {
		snap := fixture("r")
		edit(&snap)
		if _, e := s.Start(ctx, snap, "key"); e == nil {
			t.Fatal("invalid start accepted")
		}
	}
	if _, e := s.Start(ctx, fixture("r"), ""); e == nil {
		t.Fatal("empty key accepted")
	}
	rows, e := s.List(ctx, 10, 0)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal("valid retry blocked", e)
	}
	t.Log("malformed starts leave no rows or active reservation")
}

func TestCorruptPersistedProjection(t *testing.T) {
	for _, kind := range []string{"completion JSON", "completion counts", "base replacement", "CPU base"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, _ := openTest(t)
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			cp := checkpoint(1)
			cp.StartedAt = time.Unix(1000, 0).UTC()
			cp.ElapsedNS = int64(time.Second)
			cp.CompletionThrough = 1
			cp.CompletionBatches = []engine.CompletionBatch{batch(1, 0)}
			if e := s.Commit(ctx, cp); e != nil {
				t.Fatal(e)
			}
			if kind == "completion JSON" || kind == "completion counts" {
				data := []byte(`{`)
				if kind == "completion counts" {
					v := engine.CompletionValue{}
					v.Attempts[0] = 1
					var e error
					data, e = json.Marshal(v)
					if e != nil {
						t.Fatal(e)
					}
				}
				if _, e := s.writer.Exec("UPDATE completion_buckets SET data=?", data); e != nil {
					t.Fatal(e)
				}
				if _, e := s.ReadReportPage(ctx, "r", ReportQuery{Limit: 1}); e == nil {
					t.Fatal("corrupt completion projected")
				}
			} else {
				if _, e := s.writer.Exec("UPDATE buckets SET data='{' WHERE template=''"); e != nil {
					t.Fatal(e)
				}
				next := checkpoint(2)
				next.StartedAt = cp.StartedAt
				next.CompletionThrough = 1
				if kind == "CPU base" {
					next.Buckets = nil
					percent := 35.0
					next.CPUAmendments = []engine.CPUAmendment{{Index: 0, Identity: "cpu", At: cp.StartedAt.Add(100 * time.Millisecond), Percent: &percent}}
				}
				if e := s.Commit(ctx, next); e == nil {
					t.Fatal("corrupt base replaced")
				}
			}
			var seq string
			if e := s.writer.QueryRow("SELECT seq FROM runs WHERE id='r'").Scan(&seq); e != nil || seq != "1" {
				t.Fatal(seq, e)
			}
			t.Log("corrupt completion/base rejected with unchanged checkpoint cursor")
		})
	}
}
