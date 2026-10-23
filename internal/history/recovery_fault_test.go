package history

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestRecoveryFaultRepair(t *testing.T) {
	// original evidence -> corrupt/abort -> unchanged row -> repaired retry
	for _, fault := range []string{"snapshot", "checkpoint", "created", "update"} {
		t.Run(fault, func(t *testing.T) {
			s, _ := openTest(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.Start(ctx, fixture("r"), "key"); err != nil {
				t.Fatal(err)
			}
			cp := checkpoint(2)
			if err := s.Commit(ctx, cp); err != nil {
				t.Fatal(err)
			}
			raw := func() []any {
				t.Helper()
				var active int
				var state, reason, seq, created string
				var snapshot, checkpoint, recovery, counts []byte
				if err := s.reader.QueryRowContext(ctx, "SELECT active,state,reason,seq,created,snapshot,checkpoint,recovery,counts FROM runs WHERE id='r'").Scan(&active, &state, &reason, &seq, &created, &snapshot, &checkpoint, &recovery, &counts); err != nil {
					t.Fatal(err)
				}
				return []any{active, state, reason, seq, created, snapshot, checkpoint, recovery, counts}
			}
			original := raw()
			statement := "CREATE TRIGGER fail_recover BEFORE UPDATE OF recovery ON runs BEGIN SELECT RAISE(ABORT,'recovery fault'); END"
			if fault != "update" {
				statement = "UPDATE runs SET " + fault + "='broken' WHERE id='r'"
			}
			if _, err := s.writer.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
			damaged := raw()
			detected := time.Unix(3000, 0).UTC()
			if ids, err := s.Recover(ctx, detected); err == nil || len(ids) != 0 {
				t.Fatalf("fault accepted: %v %v", ids, err)
			}
			if got := raw(); !reflect.DeepEqual(got, damaged) {
				t.Fatalf("failed recovery mutated row: %v", got)
			}
			if fault == "update" {
				if _, err := s.writer.ExecContext(ctx, "DROP TRIGGER fail_recover"); err != nil {
					t.Fatal(err)
				}
			} else {
				index := map[string]int{"created": 4, "snapshot": 5, "checkpoint": 6}[fault]
				if _, err := s.writer.ExecContext(ctx, "UPDATE runs SET "+fault+"=? WHERE id='r'", original[index]); err != nil {
					t.Fatal("reader/transaction not released", err)
				}
			}
			if !reflect.DeepEqual(raw(), original) {
				t.Fatal("repair changed unrelated evidence")
			}
			before, err := s.ReadReport(ctx, "r")
			if err != nil {
				t.Fatal(err)
			}
			if ids, err := s.Recover(ctx, detected); err != nil || !reflect.DeepEqual(ids, []string{"r"}) {
				t.Fatal(ids, err)
			}
			after, err := s.ReadReport(ctx, "r")
			if err != nil {
				t.Fatal(err)
			}
			if after.State != "interrupted" || after.LatestSequence != before.LatestSequence || !reflect.DeepEqual(after.Checkpoint, before.Checkpoint) || !reflect.DeepEqual(after.Buckets, before.Buckets) || !reflect.DeepEqual(after.Aggregates, before.Aggregates) || !reflect.DeepEqual(after.Samples, before.Samples) || after.Recovery == nil || after.Recovery.UnobservedFrom != cp.Cut || !reflect.DeepEqual(after.Recovery.Unknown, cp.Unfinished) || after.Recovery.DetectedAt != detected {
				t.Fatal("recovery changed durable observations", after)
			}
			if ids, err := s.Recover(ctx, detected.Add(time.Second)); err != nil || len(ids) != 0 {
				t.Fatal(ids, err)
			}
			again, err := s.ReadReport(ctx, "r")
			if err != nil || !reflect.DeepEqual(after, again) {
				t.Fatal("second recovery changed evidence", err)
			}
			t.Logf("fault=%s original cut/unknown identity retained; rollback and once-only retry verified without traffic", fault)
		})
	}
}
