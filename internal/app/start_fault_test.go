package app

import (
	"context"
	"database/sql"
	"errors"
	"highloader/internal/api"
	"highloader/internal/engine"
	"highloader/internal/history"
	"sync/atomic"
	"testing"
)

func TestStartFaultCleanupRetry(t *testing.T) {
	// rejected start -> no durable reservation -> repair -> accepted retry -> join
	for _, fault := range []string{"preflight", "transport", "transaction"} {
		t.Run(fault, func(t *testing.T) {
			var created, closed, sends atomic.Int64
			rejectTransport := fault == "transport"
			a, path := fixture(t, "http://127.0.0.1:1", func(o *Options) {
				o.NewTransport = func(int) (engine.Transport, func()) {
					created.Add(1)
					close := func() { closed.Add(1) }
					if rejectTransport {
						return nil, close
					}
					return countingTransport{inner: retentionTransport{}, count: &sends}, close
				}
			})
			ctx := context.Background()
			valid := a.Draft()
			if fault == "preflight" {
				invalid := editor(valid)
				invalid.URL = ""
				if _, err := a.Update(valid.Revision, invalid); err != nil {
					t.Fatal(err)
				}
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if fault == "transaction" {
				if _, err = db.Exec("CREATE TRIGGER reject_start BEFORE INSERT ON runs BEGIN SELECT RAISE(ABORT,'start fault'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = a.Start(ctx, a.Draft().Revision, "retry-key"); err == nil {
				t.Fatal("fault accepted")
			}
			if fault == "preflight" {
				var ae *api.Error
				if !errors.As(err, &ae) || ae.Code != "preflight" {
					t.Fatal(err)
				}
			}
			want := int64(1)
			if fault == "preflight" {
				want = 0
			}
			if created.Load() != want || closed.Load() != want || sends.Load() != 0 || a.Active().RunID != nil || a.reserved {
				t.Fatalf("failure ownership created=%d closed=%d sends=%d reserved=%v", created.Load(), closed.Load(), sends.Load(), a.reserved)
			}
			if _, err = a.options.Store.FindStart(ctx, "retry-key"); !errors.Is(err, history.ErrNotFound) {
				t.Fatal("idempotency reservation survived rollback", err)
			}
			rows, err := a.options.Store.List(ctx, 10, 0)
			if err != nil || len(rows) != 0 {
				t.Fatal(rows, err)
			}
			if fault == "transaction" {
				if _, err = db.Exec("DROP TRIGGER reject_start"); err != nil {
					t.Fatal(err)
				}
			}
			rejectTransport = false
			if fault == "preflight" {
				if _, err = a.Update(a.Draft().Revision, editor(valid)); err != nil {
					t.Fatal(err)
				}
			}
			started, err := a.Start(ctx, a.Draft().Revision, "retry-key")
			if err != nil {
				t.Fatal("reservation not released", err)
			}
			if _, err = a.Stop(ctx, *started.RunID, true); err != nil {
				t.Fatal(err)
			}
			await(t, a.active.released)
			if !a.active.runner.View().Durable || created.Load() != want+1 || closed.Load() != want+1 {
				t.Fatal("retry did not durably release resources")
			}
			rows, err = a.options.Store.List(ctx, 10, 0)
			if err != nil || len(rows) != 1 || rows[0].RunID != *started.RunID {
				t.Fatal(rows, err)
			}
			retry, err := a.Start(ctx, 0, "retry-key")
			if err != nil || *retry.RunID != *started.RunID || created.Load() != want+1 {
				t.Fatal("same-key retry duplicated setup", err)
			}
			t.Logf("fault=%s zero precommit sends/storage; exact cleanup and same-key durable retry; SQL abort is not committed-but-unacknowledged", fault)
		})
	}
}
