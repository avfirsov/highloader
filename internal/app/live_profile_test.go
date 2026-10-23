package app

import (
	"context"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"testing"
	"time"
)

func TestLiveProfileAfterDraftAndRestart(t *testing.T) {
	for _, mode := range []string{"open", "closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, path := fixture(t, "http://127.0.0.1:1", nil)
			d := a.Draft()
			d.Profile = domain.Profile{Mode: mode, RetryAccounting: "above", Steps: []domain.Step{{DurationSeconds: 60, Level: 1}}}
			if _, err := a.Update(d.Revision, editor(d)); err != nil {
				t.Fatal(err)
			}
			active, err := a.Start(ctx, a.Draft().Revision, "profile")
			if err != nil {
				t.Fatal(err)
			}
			check := func(v engine.View) {
				t.Helper()
				b, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				var wire map[string]any
				if err = json.Unmarshal(b, &wire); err != nil {
					t.Fatal(err)
				}
				if wire["mode"] != mode || wire["retryAccounting"] != "above" {
					t.Fatalf("old view reinterpreted: mode=%v accounting=%v", wire["mode"], wire["retryAccounting"])
				}
			}
			check(*active.View)
			if _, err = a.Stop(ctx, *active.RunID, false); err != nil {
				t.Fatal(err)
			}
			await(t, a.active.released)
			check(*a.Active().View)
			next := a.Draft()
			next.Profile.Mode, next.Profile.RetryAccounting = "open", "within"
			if mode == "open" {
				next.Profile.Mode = "closed"
			}
			if _, err = a.Update(next.Revision, editor(next)); err != nil {
				t.Fatal(err)
			}
			check(*a.Active().View)
			other := domain.RunSnapshot{SchemaVersion: 1, RunID: "repeat-source", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Draft: a.Draft()}
			if _, err = a.options.Store.Start(ctx, other, "repeat-source"); err != nil {
				t.Fatal(err)
			}
			if err = a.options.Store.Commit(ctx, engine.Checkpoint{RunID: other.RunID, Sequence: 1, Cut: time.Now().UTC(), State: "completed", Final: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Repeat(ctx, other.RunID, a.Draft().Revision); err != nil {
				t.Fatal(err)
			}
			check(*a.Active().View)
			if err = a.Close(ctx); err != nil {
				t.Fatal(err)
			}
			store, err := history.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			b, err := New(Options{Store: store, PickFolder: func() (string, bool, error) { return "", true, nil }})
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			defer b.Close(ctx)
			stored, err := b.View(ctx, *active.RunID)
			if err != nil {
				t.Fatal(err)
			}
			check(stored)
			t.Log("Draft edit and Repeat preserve active profile; reopened store reconstructs the same metadata")
		})
	}
}
