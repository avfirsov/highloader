package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(id string) domain.RunSnapshot {
	d := domain.NewDraft()
	d.URL = "http://127.0.0.1/example"
	d.Auth = domain.Auth{Mode: "basic", Username: "user", Password: "private"}
	d.Templates = []domain.TemplateSource{{ID: "t", Filename: "original.jsonc", Raw: []byte("// original\n{\"n\":9007199254740993}"), Body: json.RawMessage(`{"n":9007199254740993}`), Valid: true}}
	d.SelectedIDs = []string{"t"}
	return domain.RunSnapshot{SchemaVersion: 1, RunID: id, CreatedAt: time.Unix(1000, 0).UTC().Format(time.RFC3339Nano), Draft: d}
}

func checkpoint(seq uint64) engine.Checkpoint {
	return engine.Checkpoint{RunID: "r", Sequence: seq, Cut: time.Unix(1000+int64(seq), 0).UTC(), State: "running", Buckets: []engine.Bucket{{Index: 0, StartNS: 0, EndNS: 500000000, Initial: seq}}, Aggregates: []engine.Dimension{{Kind: "overall", Stage: -1, Aggregate: engine.Aggregate{Initial: seq}}}, Samples: []engine.Sample{{RunID: "r", Sequence: 1, Attempt: 1, Request: metrics.Body{Data: []byte("actual"), Present: true}}}, Unfinished: []engine.Unfinished{{Sequence: 9, Attempt: 1, Active: true}}}
}

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestHistoryScenario(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	original := fixture("r")
	result, e := s.Start(ctx, original, "key")
	if e != nil || result.Existing {
		t.Fatal(result, e)
	}
	original.Draft.Auth.Password = "mutated"
	original.Draft.Templates[0].Raw[0] = '!'
	if _, e = s.Start(ctx, fixture("other"), "other-key"); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, "r"); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	if e = s.Clear(ctx); !errors.Is(e, ErrActive) {
		t.Fatal(e)
	}
	if e = s.Commit(ctx, checkpoint(1)); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(2)
	cp.Buckets = append(cp.Buckets, engine.Bucket{Index: 1, StartNS: 500000000, EndNS: 750000000})
	if e = s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	report, e := s.ReadReport(ctx, "r")
	if e != nil || len(report.Buckets) != 2 || report.Buckets[1].EndNS != 750000000 || len(report.Samples) != 1 || report.Aggregates[0].Aggregate.Initial != 2 {
		t.Fatalf("report=%+v err=%v", report, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	detection := time.Unix(2000, 0).UTC()
	ids, e := s.Recover(ctx, detection)
	if e != nil || len(ids) != 1 {
		t.Fatal(ids, e)
	}
	r, e := s.Get(ctx, "r")
	if e != nil || r.State != "interrupted" || r.Checkpoint.Sequence != 2 || r.Recovery.UnobservedFrom != cp.Cut || len(r.Recovery.Unknown) != 1 || r.Recovery.DetectedAt != detection {
		t.Fatal(r, e)
	}
	d, e := s.Repeat(ctx, "r")
	if e != nil || d.Auth.Password != "private" || !reflect.DeepEqual(d.Templates, fixture("r").Draft.Templates) {
		t.Fatal("source fidelity", e)
	}
	if !strings.Contains(string(d.Templates[0].Body), "9007199254740993") {
		t.Fatal("numeric precision lost")
	}
	result, e = s.Start(ctx, fixture("replacement"), "key")
	if e != nil || !result.Existing || result.RunID != "r" {
		t.Fatal(result, e)
	}
	rows, e := s.List(ctx, 1, 0)
	if e != nil || len(rows) != 1 || rows[0].State != "interrupted" {
		t.Fatal(rows, e)
	}
	buckets, e := s.Buckets(ctx, "r", 1, 1)
	if e != nil || len(buckets) != 1 || buckets[0].Index != 1 {
		t.Fatal(buckets, e)
	}
	if e = s.Delete(ctx, "r"); e != nil {
		t.Fatal(e)
	}
	buckets, e = s.Buckets(ctx, "r", 0, 10)
	if e != nil || len(buckets) != 0 {
		t.Fatal(buckets, e)
	}
	t.Log("Source fidelity, partial bucket, checkpoint provenance, unknown work and cascade verified")
}

func TestTransactionFaults(t *testing.T) {
	for _, boundary := range []string{"begin", "after_evidence", "before_commit", "after_commit"} {
		t.Run(boundary, func(t *testing.T) {
			ctx := context.Background()
			s, path := openTest(t)
			if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
				t.Fatal(e)
			}
			if e := s.Commit(ctx, checkpoint(1)); e != nil {
				t.Fatal(e)
			}
			injected := errors.New("injected failure")
			s.fault = func(at string) error {
				if at == boundary {
					return injected
				}
				return nil
			}
			if e := s.Commit(ctx, checkpoint(2)); !errors.Is(e, injected) {
				t.Fatal(e)
			}
			s.fault = nil
			s.Close()
			reopened, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			report, e := reopened.ReadReport(ctx, "r")
			if e != nil {
				t.Fatal(e)
			}
			expected := uint64(1)
			if boundary == "after_commit" {
				expected = 2
			}
			if report.LatestSequence != expected || report.Checkpoint.Sequence != expected || report.Buckets[0].Initial != expected || report.Aggregates[0].Aggregate.Initial != expected || len(report.Samples) != 1 {
				t.Fatalf("mixed transaction: %+v", report)
			}
			t.Logf("boundary=%s durable_sequence=%d", boundary, expected)
		})
	}
}

func TestRevisionGuards(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(2)
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	changed := cp
	changed.Reason = "changed"
	if e := s.Commit(ctx, changed); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e := s.Commit(ctx, checkpoint(1)); !errors.Is(e, ErrStale) {
		t.Fatal(e)
	}
	cp.Sequence = ^uint64(0)
	cp.Final = true
	cp.State = "completed"
	cp.Unfinished = nil
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(ctx, "r")
	if e != nil || r.LatestSequence != ^uint64(0) {
		t.Fatal(r, e)
	}
	if e = s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	if e = s.Clear(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, "r"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	s.Close()
	if e = s.Commit(ctx, checkpoint(1)); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	t.Log("Duplicate, stale, full-width sequence, terminal and closed guards")
}

func TestSchemaProtection(t *testing.T) {
	for _, version := range []int{0, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.sqlite")
			db, e := sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec("CREATE TABLE evidence(value TEXT); INSERT INTO evidence VALUES('keep')"); e != nil {
				t.Fatal(e)
			}
			if version == 2 {
				if _, e = db.Exec("PRAGMA user_version=2"); e != nil {
					t.Fatal(e)
				}
			}
			db.Close()
			if s, e := Open(path); !errors.Is(e, ErrSchema) {
				if s != nil {
					s.Close()
				}
				t.Fatal(e)
			}
			db, e = sql.Open("sqlite", path)
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var value string
			if e = db.QueryRow("SELECT value FROM evidence").Scan(&value); e != nil || value != "keep" {
				t.Fatal(value, e)
			}
			t.Log("Unsupported evidence preserved")
		})
	}
}

func TestStartFaults(t *testing.T) {
	for _, boundary := range []string{"before_commit", "after_commit"} {
		t.Run(boundary, func(t *testing.T) {
			s, _ := openTest(t)
			ctx := context.Background()
			s.fault = func(at string) error {
				if at == boundary {
					return errors.New("injected start")
				}
				return nil
			}
			if _, e := s.Start(ctx, fixture("r"), "key"); e == nil {
				t.Fatal("expected failure")
			}
			s.fault = nil
			result, e := s.Start(ctx, fixture("r"), "key")
			if e != nil || result.Existing != (boundary == "after_commit") {
				t.Fatal(result, e)
			}
			rows, e := s.List(ctx, 10, 0)
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e)
			}
			t.Logf("boundary=%s unique durable start", boundary)
		})
	}
}

func TestSQLFailureRollsBackEvidence(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	if e := s.Commit(ctx, checkpoint(1)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.writer.Exec("CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF checkpoint ON runs BEGIN SELECT RAISE(ABORT,'injected sql failure'); END"); e != nil {
		t.Fatal(e)
	}
	if e := s.Commit(ctx, checkpoint(2)); e == nil {
		t.Fatal("expected SQL failure")
	}
	r, e := s.ReadReport(ctx, "r")
	if e != nil || r.LatestSequence != 1 || r.Buckets[0].Initial != 1 || r.Aggregates[0].Aggregate.Initial != 1 {
		t.Fatal(r, e)
	}
	t.Log("SQL abort leaves complete previous checkpoint")
}

func TestConcurrentStarts(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		go func(id string) { <-gate; _, e := s.Start(ctx, fixture(id), id); results <- e }(id)
	}
	close(gate)
	success, active := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if errors.Is(e, ErrActive) {
			active++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || active != 1 {
		t.Fatal(success, active)
	}
	t.Log("Exactly one concurrent start committed")
}

func TestRecoveryWithoutCheckpoint(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	snap := fixture("r")
	if _, e := s.Start(ctx, snap, "key"); e != nil {
		t.Fatal(e)
	}
	when := time.Unix(3000, 0).UTC()
	if _, e := s.Recover(ctx, when); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(ctx, "r")
	if e != nil || r.Checkpoint != nil || r.DurableCut != nil || r.Recovery == nil || r.State != "interrupted" || len(r.Recovery.Unknown) != 0 {
		t.Fatal(r, e)
	}
	if ids, e := s.Recover(ctx, when.Add(time.Second)); e != nil || len(ids) != 0 {
		t.Fatal(ids, e)
	}
	t.Log("Missing checkpoint and unknown interval remain explicit")
}

func TestConnectionSettingsAndCanceledWrite(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	connections := []*sql.Conn{}
	for i := 0; i < 4; i++ {
		c, e := s.reader.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		connections = append(connections, c)
		defer c.Close()
		var syncMode, fk, readOnly int
		var journal string
		if e = c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&syncMode); e != nil {
			t.Fatal(e)
		}
		if e = c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); e != nil {
			t.Fatal(e)
		}
		if e = c.QueryRowContext(ctx, "PRAGMA query_only").Scan(&readOnly); e != nil {
			t.Fatal(e)
		}
		if e = c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); e != nil {
			t.Fatal(e)
		}
		if syncMode != 2 || fk != 1 || readOnly != 1 || journal != "wal" {
			t.Fatal(syncMode, fk, readOnly, journal)
		}
	}
	for _, c := range connections {
		c.Close()
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.Start(canceled, fixture("r"), "key"); e == nil {
		t.Fatal("canceled start succeeded")
	}
	rows, e := s.List(ctx, 10, 0)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	t.Log("All readers configured; canceled transaction absent")
}

func TestFileURI(t *testing.T) {
	u := fileURI(`D:/space #?%/history.sqlite`)
	if got := u.String(); got != "file:///D:/space%20%23%3F%25/history.sqlite" {
		t.Fatal(got)
	}
	path := filepath.Join(t.TempDir(), "history #%.sqlite")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Start(context.Background(), fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	t.Log("Drive letter stays in path; space, hash and percent round trip")
}

func TestBucketScopesAndSampleNumbers(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Start(ctx, fixture("r"), "key"); e != nil {
		t.Fatal(e)
	}
	cp := checkpoint(1)
	cp.Buckets = []engine.Bucket{{Index: 0, EndNS: 50, Initial: 3}, {TemplateID: "A", Index: 0, EndNS: 50, Initial: 1}, {TemplateID: "B", Index: 0, EndNS: 50, Initial: 2}}
	cp.Samples[0].Sequence = 9007199254740993
	if e := s.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	all, e := s.ReadReport(ctx, "r")
	if e != nil || len(all.Buckets) != 3 || all.Samples[0].Sequence != 9007199254740993 {
		t.Fatal(all, e)
	}
	for scope, want := range map[string]uint64{"": 3, "A": 1, "B": 2} {
		rows, e := s.BucketsForTemplate(ctx, "r", scope, 0, 10)
		if e != nil || len(rows) != 1 || rows[0].Initial != want {
			t.Fatal(scope, rows, e)
		}
	}
	overall, e := s.Buckets(ctx, "r", 0, 10)
	if e != nil || len(overall) != 1 || overall[0].TemplateID != "" {
		t.Fatal(overall, e)
	}
	cp.Sequence = 2
	cp.Buckets = append(cp.Buckets, cp.Buckets[1])
	if e = s.Commit(ctx, cp); e == nil {
		t.Fatal("duplicate scope accepted")
	}
	wire, e := json.Marshal(all.Samples)
	if e != nil || !strings.Contains(string(wire), `"9007199254740993"`) {
		t.Fatal(string(wire), e)
	}
	t.Log("Three scopes and decimal sample identities preserved")
}

func TestCPUAmendment(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if _, err := s.Start(ctx, fixture("r"), "cpu"); err != nil {
		t.Fatal(err)
	}
	origin := time.Unix(1000, 0)
	users := int64(3)
	cp := checkpoint(1)
	cp.StartedAt = origin
	cp.ElapsedNS = int64(time.Second)
	cp.Buckets[0].TargetUsers = &users
	cp.Buckets[0].SnapshotAt = origin.Add(100 * time.Millisecond)
	if err := s.Commit(ctx, cp); err != nil {
		t.Fatal(err)
	}
	percent := 42.0
	amendment := engine.CPUAmendment{Index: 0, At: origin.Add(499 * time.Millisecond), Identity: "late", Percent: &percent}
	cp = checkpoint(2)
	cp.StartedAt = origin
	cp.ElapsedNS = int64(2 * time.Second)
	cp.Buckets = nil
	cp.CPUAmendments = []engine.CPUAmendment{amendment}
	if err := s.Commit(ctx, cp); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx, cp); err != nil {
		t.Fatal("replay", err)
	}
	cp.Sequence = 3
	cp.Cut = origin.Add(3 * time.Second)
	cp.CPUAmendments = nil
	cp.Buckets = checkpoint(1).Buckets
	cp.Buckets[0].Initial = 9
	cp.Buckets[0].TargetUsers = &users
	cp.Buckets[0].SnapshotAt = origin.Add(100 * time.Millisecond)
	if err := s.Commit(ctx, cp); err != nil {
		t.Fatal(err)
	}
	report, err := s.ReadReport(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	b := report.Buckets[0]
	if b.Initial != 9 || b.CPU == nil || *b.CPU != 42 || b.CPUIdentity != "late" || b.TargetUsers == nil || *b.TargetUsers != 3 || !b.SnapshotAt.Equal(origin.Add(100*time.Millisecond)) {
		t.Fatal(b)
	}
	cp.Sequence = 4
	cp.Buckets = nil
	amendment.Index = 1
	amendment.At = origin.Add(1500 * time.Millisecond)
	cp.CPUAmendments = []engine.CPUAmendment{amendment}
	if err = s.Commit(ctx, cp); err == nil || !strings.Contains(err.Error(), "missing base interval") {
		t.Fatal(err)
	}
	record, err := s.Get(ctx, "r")
	if err != nil || record.LatestSequence != 3 {
		t.Fatal(record, err)
	}
	t.Log("replay idempotent; stale base keeps later CPU and real dispatch/users; missing base rolls back without fabrication")
}
