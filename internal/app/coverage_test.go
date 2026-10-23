package app

import (
	"context"
	"errors"
	"highloader/internal/api"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/report"
	"highloader/internal/request"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestImportFailureReleasesOperation(t *testing.T) {
	for _, kind := range []string{"dialog error", "dialog canceled", "context canceled", "missing directory"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := fixture(t, "http://127.0.0.1/unused", nil)
			before := a.Draft()
			ctx := context.Background()
			a.options.PickFolder = func() (string, bool, error) { return filepath.Join(t.TempDir(), "absent"), false, nil }
			switch kind {
			case "dialog error":
				a.options.PickFolder = func() (string, bool, error) { return "", false, errors.New("dialog unavailable") }
			case "dialog canceled":
				a.options.PickFolder = func() (string, bool, error) { return "", true, nil }
			case "context canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, e := a.Import(ctx, before.Revision)
			if kind == "dialog canceled" {
				if e != nil || !result.Canceled || !reflect.DeepEqual(result.Draft, before) {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("failure accepted")
			}
			if !reflect.DeepEqual(a.Draft(), before) {
				t.Fatal("failed import changed draft")
			}
			next, e := a.Update(before.Revision, editor(before))
			if e != nil || next.Revision != before.Revision+1 {
				t.Fatal("reservation retained", e)
			}
			t.Log("failed/canceled import preserved originals and released operation")
		})
	}
}

func TestDraftNormalizationAndExhaustion(t *testing.T) {
	if _, e := New(Options{}); e == nil {
		t.Fatal("missing adapters accepted")
	}
	a, _ := fixture(t, "http://127.0.0.1/unused", nil)
	before := a.Draft()
	valid := a.Validate()
	if len(valid.Diagnostics) != 0 || len(valid.Effective) != 1 {
		t.Fatal(valid)
	}
	edited, e := a.Update(before.Revision, api.EditableDraft{Settings: map[string]domain.TemplateSettings{"t": {}}})
	if e != nil {
		t.Fatal(e)
	}
	if edited.SelectedIDs == nil || edited.Headers == nil || edited.Profile.Steps == nil || edited.Policy.Statuses == nil || edited.Settings["t"].Headers == nil || !reflect.DeepEqual(edited.Templates, before.Templates) {
		t.Fatal(edited)
	}
	invalid := a.Validate()
	if invalid.Effective == nil || len(invalid.Diagnostics) == 0 {
		t.Fatal(invalid)
	}
	a.mu.Lock()
	a.draft.Revision = math.MaxUint64
	a.mu.Unlock()
	_, e = a.Update(math.MaxUint64, api.EditableDraft{})
	var boundary *api.Error
	if !errors.As(e, &boundary) || boundary.Code != "revision_exhausted" || a.Draft().Revision != math.MaxUint64 {
		t.Fatal(e)
	}
	// A failed update must release reservation even when another edit cannot advance the exhausted token.
	if e = a.Clear(context.Background()); e != nil {
		t.Fatal("reservation retained", e)
	}
	t.Log("empty arrays stable, originals protected, overflow rejected without stuck reservation")
}

func TestEvidenceErrors(t *testing.T) {
	ctx := context.Background()
	a, _ := fixture(t, "http://127.0.0.1/unused", nil)
	if _, e := a.Repeat(ctx, "missing", a.Draft().Revision); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := a.Update(a.Draft().Revision, editor(a.Draft())); e != nil {
		t.Fatal("repeat retained reservation", e)
	}
	if _, e := a.Report(ctx, "missing", report.Options{}); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := a.Compare(ctx, "missing", "other", nil); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := a.Export(ctx, api.Selection{RunID: "missing"}); e == nil {
		t.Fatal("missing report assets accepted")
	}
	a.options.ReportJS = []byte("/* test renderer */")
	a.options.ReportCSS = []byte("/* test style */")
	if _, e := a.Export(ctx, api.Selection{LeftID: "missing", RightID: "other"}); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	if e := a.options.Store.Close(); e != nil {
		t.Fatal(e)
	}
	page, e := a.History(ctx, 1, 0)
	if e == nil || page.Items == nil || page.NextOffset != nil {
		t.Fatal(page, e)
	}
	t.Log("missing evidence and closed store propagate; failed repeat releases preparation")
}

func TestStoredReportOperationErrors(t *testing.T) {
	ctx := context.Background()
	a, _ := fixture(t, "http://127.0.0.1/unused", nil)
	now := time.Unix(1700000000, 0).UTC()
	for _, id := range []string{"left", "right"} {
		snap, e := request.Freeze(a.Draft(), id, "test", now)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.options.Store.Start(ctx, snap, id); e != nil {
			t.Fatal(e)
		}
		cp := engine.Checkpoint{RunID: id, Sequence: 1, Cut: now.Add(time.Second), State: "completed", Final: true, Aggregates: []engine.Dimension{{Kind: "overall", Aggregate: engine.Aggregate{Initial: 3}}}}
		if e = a.options.Store.Commit(ctx, cp); e != nil {
			t.Fatal(e)
		}
	}
	for _, force := range []bool{false, true} {
		v, e := a.Stop(ctx, "left", force)
		if e != nil || v.State != "completed" || !v.Durable || !v.WorkersReleased || v.RunID != "left" {
			t.Fatal(v, e)
		}
	}
	if _, e := a.Stop(ctx, "missing", false); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	first, e := a.History(ctx, 1, 0)
	if e != nil || len(first.Items) != 1 || first.Items[0].RunID != "left" || first.NextOffset == nil || *first.NextOffset != 1 {
		t.Fatal(first, e)
	}
	second, e := a.History(ctx, 2, 1)
	if e != nil || len(second.Items) != 1 || second.Items[0].RunID != "right" || second.NextOffset != nil {
		t.Fatal(second, e)
	}
	if _, e := a.Report(ctx, "left", report.Options{TemplateID: "missing"}); e == nil {
		t.Fatal("unknown scope accepted")
	} else {
		var v *api.Error
		if !errors.As(e, &v) || v.Code != "invalid_report" || v.Status != 422 {
			t.Fatal(e)
		}
	}
	if _, e := a.Compare(ctx, "left", "missing", nil); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := a.Compare(ctx, "left", "right", map[string]string{"missing": "other"}); e == nil {
		t.Fatal("invalid mapping accepted")
	} else {
		var v *api.Error
		if !errors.As(e, &v) || v.Code != "invalid_mapping" {
			t.Fatal(e)
		}
	}
	pin := uint64(0)
	if _, e := a.Report(ctx, "left", report.Options{ExpectedSequence: &pin}); !errors.Is(e, history.ErrStale) {
		t.Fatal(e)
	}
	a.options.ReportJS = []byte("test")
	a.options.ReportCSS = []byte("test")
	if _, e := a.Export(ctx, api.Selection{LeftID: "left", RightID: "missing"}); !errors.Is(e, history.ErrNotFound) {
		t.Fatal(e)
	}
	html, e := a.Export(ctx, api.Selection{LeftID: "left", RightID: "right"})
	if e != nil || !strings.Contains(string(html), "highloader-report-data") {
		t.Fatal(e)
	}
	t.Log("real historical pages, invalid scopes/mappings and stale pins preserve truthful service outcomes")
}

func TestRejectedStartAndReservedPreparation(t *testing.T) {
	a, _ := fixture(t, "http://127.0.0.1/unused", nil)
	ctx := context.Background()
	for _, key := range []string{"", strings.Repeat("k", 201)} {
		if _, e := a.Start(ctx, a.Draft().Revision, key); e == nil {
			t.Fatal("invalid key accepted")
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	a.options.PickFolder = func() (string, bool, error) { close(entered); <-release; return "", true, nil }
	go func() { _, e := a.Import(ctx, a.Draft().Revision); done <- e }()
	await(t, entered)
	// Always release before assertions can abort the test and join the application cleanup.
	_, importErr := a.Import(ctx, a.Draft().Revision)
	_, repeatErr := a.Repeat(ctx, "missing", a.Draft().Revision)
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	for _, e := range []error{importErr, repeatErr} {
		var v *api.Error
		if !errors.As(e, &v) || v.Code != "reserved" {
			t.Fatal(e)
		}
	}
	if _, e := a.Update(a.Draft().Revision, api.EditableDraft{}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Start(ctx, a.Draft().Revision, "invalid-draft"); e == nil {
		t.Fatal("preflight bypassed")
	}
	page, e := a.History(ctx, 10, 0)
	if e != nil || len(page.Items) != 0 {
		t.Fatal("rejected starts persisted", page, e)
	}
	t.Log("barrier proves competing imports/repeats reject; failed preflight sends nothing and stores nothing")
}

func TestClosedAndClosingAdmission(t *testing.T) {
	exited := make(chan struct{})
	a, _ := fixture(t, "http://127.0.0.1/unused", func(o *Options) { o.RequestExit = func() { close(exited) } })
	a.Exit()
	await(t, exited)
	_, e := a.Update(a.Draft().Revision, editor(a.Draft()))
	var boundary *api.Error
	if !errors.As(e, &boundary) || boundary.Code != "closing" {
		t.Fatal(e)
	}
	if e := a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Start(context.Background(), a.Draft().Revision, "closed-store"); e == nil {
		t.Fatal("closed store start succeeded")
	}
	if a.Active().RunID != nil {
		t.Fatal("closed start published run")
	}
	t.Log("exit closes edits; closed history cannot publish or dispatch a start")
}
