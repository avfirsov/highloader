package acceptance

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/request"
	"highloader/internal/transport"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type crashSink struct {
	store      *history.Store
	path, mode string
}

func (s *crashSink) Commit(ctx context.Context, cp engine.Checkpoint) error {
	if err := s.store.Commit(ctx, cp); err != nil {
		return err
	}
	if s.mode == "uncommitted" {
		db, err := sql.Open("sqlite", s.path)
		if err != nil {
			return err
		}
		defer db.Close()
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.Exec("UPDATE runs SET reason='uncommitted-must-rollback' WHERE id=?", cp.RunID); err != nil {
			return err
		}
		fmt.Printf("BARRIER %d %s\n", cp.Sequence, cp.Cut.Format(time.RFC3339Nano))
		select {}
	}
	fmt.Printf("BARRIER %d %s\n", cp.Sequence, cp.Cut.Format(time.RFC3339Nano))
	select {}
}

func TestRecoveryChild(t *testing.T) {
	if os.Getenv("HIGHLOADER_CRASH_CHILD") != "1" {
		t.Skip("owned process helper")
	}
	path := os.Getenv("HIGHLOADER_CRASH_DB")
	raw, err := os.ReadFile(os.Getenv("HIGHLOADER_CRASH_SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	d := domain.NewDraft()
	d.URL = os.Getenv("HIGHLOADER_CRASH_URL")
	d.Capacity = 1
	d.Profile.Mode = "closed"
	d.Profile.Steps = []domain.Step{{DurationSeconds: 60, Level: 1}}
	d.Templates = []domain.TemplateSource{{ID: "original", Filename: "original.json", Raw: raw, Body: json.RawMessage(raw), Valid: true}}
	d.SelectedIDs = []string{"original"}
	d.Settings["original"] = domain.TemplateSettings{Weight: 1}
	snapshot, err := request.Freeze(d, "crash-run", "native-acceptance", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Start(context.Background(), snapshot, "crash-start"); err != nil {
		t.Fatal(err)
	}
	runner, err := engine.New(snapshot, engine.Options{Transport: transport.New(1), Sink: &crashSink{store: store, path: path, mode: os.Getenv("HIGHLOADER_CRASH_MODE")}, CommitTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeProcessRecovery(t *testing.T) {
	for _, mode := range []string{"durable", "uncommitted"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			sourceDir := filepath.Join(dir, "source")
			if err := os.Mkdir(sourceDir, 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(sourceDir, "original.json")
			original := []byte(`{"n":9007199254740993,"source":"immutable"}`)
			if err := os.WriteFile(source, original, 0600); err != nil {
				t.Fatal(err)
			}
			var observed atomic.Int64
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed.Add(1)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			dbPath := filepath.Join(dir, "history.sqlite")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryChild$", "-test.v")
			cmd.Env = append(os.Environ(), "HIGHLOADER_CRASH_CHILD=1", "HIGHLOADER_CRASH_DB="+dbPath, "HIGHLOADER_CRASH_SOURCE="+source, "HIGHLOADER_CRASH_URL="+server.URL, "HIGHLOADER_CRASH_MODE="+mode)
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			scanner := bufio.NewScanner(stdout)
			var seq uint64
			var cutText string
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "BARRIER ") {
					if _, err = fmt.Sscanf(line, "BARRIER %d %s", &seq, &cutText); err != nil {
						t.Fatal(err)
					}
					break
				}
			}
			if seq == 0 {
				t.Fatal("child barrier absent", scanner.Err())
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = cmd.Wait(); err == nil {
				t.Fatal("expected killed process exit")
			}
			cut, err := time.Parse(time.RFC3339Nano, cutText)
			if err != nil {
				t.Fatal(err)
			}
			// Remove only the exact file/directory created above; no recursive deletion or external paths.
			if err = os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(sourceDir); err != nil {
				t.Fatal(err)
			}
			for restart := 0; restart < 2; restart++ {
				store, err := history.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				ids, err := store.Recover(context.Background(), time.Now())
				if err != nil {
					store.Close()
					t.Fatal(err)
				}
				if len(ids) != 1-restart {
					store.Close()
					t.Fatal("recovery not idempotent", ids)
				}
				record, err := store.Get(context.Background(), "crash-run")
				if err != nil {
					store.Close()
					t.Fatal(err)
				}
				if record.State != "interrupted" || record.Checkpoint == nil || record.Checkpoint.Sequence != seq || !record.Checkpoint.Cut.Equal(cut) || record.Recovery == nil || !record.Recovery.UnobservedFrom.Equal(cut) || len(record.Recovery.Unknown) != 1 || record.Reason == "uncommitted-must-rollback" {
					store.Close()
					t.Fatal("inconsistent crash recovery", record)
				}
				draft, err := store.Repeat(context.Background(), "crash-run")
				store.Close()
				if err != nil || len(draft.Templates) != 1 || string(draft.Templates[0].Raw) != string(original) {
					t.Fatal("original not repeatable", err)
				}
			}
			if observed.Load() != 1 {
				t.Fatal("traffic replay or no initial dispatch", observed.Load())
			}
			t.Logf("mode=%s killedOwnedPID=%d durableSequence=%d cut=%s unknown=1 originalPreserved=true restartRepeat=2 serverSends=1", mode, cmd.Process.Pid, seq, cutText)
		})
	}
}
