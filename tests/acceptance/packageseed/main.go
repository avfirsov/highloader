package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/request"
	"highloader/internal/template"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	descriptor := flag.String("fixture", "", "fixture.json written by packagefixture")
	flag.Parse()
	b, err := os.ReadFile(*descriptor)
	if err != nil {
		log.Fatal(err)
	}
	var fixture struct{ Directory, Templates, FastURL string }
	if err = json.Unmarshal(b, &fixture); err != nil {
		log.Fatal(err)
	}
	if fixture.Directory == "" || fixture.Templates == "" || fixture.FastURL == "" {
		log.Fatal("incomplete fixture descriptor")
	}
	dir, err := os.MkdirTemp(fixture.Directory, "seeded-app-")
	if err != nil {
		log.Fatal(err)
	}
	imported, err := template.ImportDirectory(fixture.Templates)
	if err != nil {
		log.Fatal(err)
	}
	if len(imported.SelectedIDs) != 10 {
		log.Fatal("expected ten valid fixture templates")
	}
	draft := domain.NewDraft()
	draft.URL = fixture.FastURL
	draft.Templates, draft.SelectedIDs, draft.Settings = imported.Templates, imported.SelectedIDs, imported.Settings
	draft.Profile.Steps = []domain.Step{{DurationSeconds: 10, Level: 500}, {DurationSeconds: 60, Level: 2000}}
	now := time.Now().UTC()
	snapshot, err := request.Freeze(draft, "acceptance-synthetic-seed", "acceptance-seed-not-measurement", now)
	if err != nil {
		log.Fatal(err)
	}
	historyDir := filepath.Join(dir, "history")
	if err = os.Mkdir(historyDir, 0700); err != nil {
		log.Fatal(err)
	}
	store, err := history.Open(filepath.Join(historyDir, "highloader.sqlite"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	if _, err = store.Start(ctx, snapshot, "acceptance-synthetic-seed"); err != nil {
		log.Fatal(err)
	}
	if err = store.Commit(ctx, engine.Checkpoint{RunID: snapshot.RunID, Sequence: 1, Cut: now, StartedAt: now, State: "stopped", Reason: "acceptance_synthetic_seed_not_measured", Final: true}); err != nil {
		log.Fatal(err)
	}
	if err = store.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println(dir)
}
