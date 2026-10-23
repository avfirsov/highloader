package app

import (
	"context"
	"errors"
	"highloader/internal/engine"
	"highloader/internal/history"
	"testing"
)

type retentionTransport struct{}

func (retentionTransport) DoAttempt(ctx context.Context, a engine.Attempt) engine.Result {
	// permit -> cancellation -> worker release
	<-ctx.Done()
	return engine.Result{Sequence: a.Sequence, Number: a.Number}
}

func TestTerminalRetentionRetry(t *testing.T) {
	// durable terminal pair -> failed request -> evidence unchanged -> retry -> new owner
	for _, clearAll := range []bool{false, true} {
		name := "delete"
		if clearAll {
			name = "clear"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) {
				o.NewTransport = func(int) (engine.Transport, func()) { return retentionTransport{}, func() {} }
			})
			finish := func(key string) string {
				t.Helper()
				v, err := a.Start(ctx, a.Draft().Revision, key)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.Stop(ctx, *v.RunID, true); err != nil {
					t.Fatal(err)
				}
				await(t, a.active.released)
				if !a.active.runner.View().Durable {
					t.Fatal("terminal evidence not durable")
				}
				return *v.RunID
			}
			older := finish("older")
			target := finish("target")
			owner := a.active
			mutate := func(ctx context.Context) error {
				if clearAll {
					return a.Clear(ctx)
				}
				return a.Delete(ctx, target)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := mutate(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled retention: %v", err)
			}
			if a.active != owner || a.reserved {
				t.Fatal("failed retention lost owner or stranded reservation")
			}
			for _, id := range []string{older, target} {
				if _, err := a.options.Store.Get(ctx, id); err != nil {
					t.Fatal("failed retention erased evidence", err)
				}
			}
			if err := mutate(ctx); err != nil {
				t.Fatal(err)
			}
			if a.active != nil || a.reserved {
				t.Fatal("successful retention kept owner")
			}
			if _, err := a.options.Store.Get(ctx, target); !errors.Is(err, history.ErrNotFound) {
				t.Fatalf("target survived: %v", err)
			}
			rows, err := a.options.Store.List(ctx, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			if clearAll {
				if len(rows) != 0 {
					t.Fatal("clear retained rows")
				}
			} else {
				if len(rows) != 1 || rows[0].RunID != older {
					t.Fatal("delete changed unrelated run", rows)
				}
			}
			replacement := finish("replacement")
			if replacement == target || replacement == older {
				t.Fatal("replacement reused removed identity")
			}
			t.Log("canceled operation retained both runs and ownership; retry removed exact scope and replacement start finalized")
		})
	}
}
