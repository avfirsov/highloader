package integration

import (
	"context"
	"encoding/json"
	"highloader/internal/api"
	"highloader/internal/app"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/report"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinnedSchema2Reports(t *testing.T) {
	ctx := context.Background()
	store, e := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.New(app.Options{Store: store, PickFolder: func() (string, bool, error) { return "", true, nil }, ReportJS: []byte("/* TEST ONLY fixture */"), ReportCSS: []byte("/* TEST ONLY fixture */")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close(ctx) })
	handler := api.Handler(a, nil)
	d := domain.NewDraft()
	d.SelectedIDs = []string{"t"}
	d.Templates = []domain.TemplateSource{{ID: "t", Valid: true, Body: json.RawMessage(`{}`)}}
	created := time.Unix(2000, 0).UTC()
	snapshot := domain.RunSnapshot{SchemaVersion: 1, RunID: "paged", CreatedAt: created.Format(time.RFC3339Nano), Draft: d}
	if _, e = store.Start(ctx, snapshot, "key"); e != nil {
		t.Fatal(e)
	}
	cp := engine.Checkpoint{RunID: "paged", Sequence: 1, State: "running", Cut: created.Add(3 * time.Second), StartedAt: created, ElapsedNS: 3e9, Aggregates: []engine.Dimension{{Kind: "overall", Stage: -1, Aggregate: engine.Aggregate{Initial: 3}}, {Kind: "template", TemplateID: "t", Stage: -1, Aggregate: engine.Aggregate{Initial: 3}}}, Samples: []engine.Sample{{RunID: "paged", TemplateID: "t", Sequence: 1, Attempt: 1}, {RunID: "paged", TemplateID: "t", Sequence: 2, Attempt: 1}}}
	for i := int64(0); i < 3; i++ {
		cp.Buckets = append(cp.Buckets, engine.Bucket{Index: i, StartNS: i * 1e9, EndNS: (i + 1) * 1e9, Initial: 1}, engine.Bucket{TemplateID: "t", Index: i, StartNS: i * 1e9, EndNS: (i + 1) * 1e9, Initial: 1})
	}
	if e = store.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	read := func(path string) report.RunReport {
		w := call(t, handler, "GET", path, "", 200)
		var r report.RunReport
		if e = json.Unmarshal(w.Body.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	first := read("/api/v1/runs/paged/report?limit=1&sampleLimit=1&templateId=t")
	second := read("/api/v1/runs/paged/report?limit=1&sampleLimit=1&templateId=t&fromIndex=1&sampleOffset=1&revision=1")
	if len(first.Points) != 1 || len(second.Points) != 1 || first.Range.TotalBuckets != "3" || second.Range.TotalSamples != "2" || second.Samples[0].Sequence != 2 || first.Summary.Initial != second.Summary.Initial || *first.Summary.TotalRate != 1 {
		t.Fatal("page/global/scoped contract")
	}
	cp.Sequence = 2
	cp.Cut = created.Add(4 * time.Second)
	cp.ElapsedNS = 4e9
	v := engine.CompletionValue{}
	v.Attempts[1] = 1
	v.AttemptDurations[1].Add(50 * time.Millisecond)
	cp.CompletionThrough = 1
	cp.CompletionBatches = []engine.CompletionBatch{{Version: 1, Sequence: 1, Deltas: []engine.CompletionDelta{{Index: 0, Value: v}, {TemplateID: "t", Index: 0, Value: v}}}}
	if e = store.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	call(t, handler, "GET", "/api/v1/runs/paged/report?limit=1&fromIndex=1&revision=1", "", 409)
	late := read("/api/v1/runs/paged/report?limit=1&templateId=t&population=http&revision=2")
	if late.Points[0].CompletedCount != "1" || late.Points[0].ErrorCount != "1" || late.Points[0].P50MS == nil || *late.Points[0].P50MS < 50 {
		t.Fatal("late earlier completion missing")
	}
	out := call(t, handler, "POST", "/api/v1/export", `{"runId":"paged"}`, 200)
	if !strings.Contains(out.Body.String(), `"index":"2"`) || !strings.Contains(out.Body.String(), `"Sequence":"2"`) {
		t.Fatal("full export lost page")
	}
	cp.Sequence = 3
	cp.Final = true
	cp.State = "completed"
	cp.Cut = created.Add(603 * time.Second)
	cp.ElapsedNS = 603e9
	for i := int64(3); i < 603; i++ {
		cp.Buckets = append(cp.Buckets, engine.Bucket{Index: i, StartNS: i * 1e9, EndNS: (i + 1) * 1e9})
	}
	if e = store.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	snapshot.RunID = "other"
	if _, e = store.Start(ctx, snapshot, "key2"); e != nil {
		t.Fatal(e)
	}
	cp.RunID = "other"
	cp.Sequence = 1
	cp.Samples = nil
	if e = store.Commit(ctx, cp); e != nil {
		t.Fatal(e)
	}
	comp := call(t, handler, "POST", "/api/v1/compare", `{"leftId":"paged","rightId":"other"}`, 200)
	var comparison report.Comparison
	if e = json.Unmarshal(comp.Body.Bytes(), &comparison); e != nil {
		t.Fatal(e)
	}
	if len(comparison.Runs[0].Points) != 600 || comparison.Runs[0].Range.NextIndex == nil || comparison.Runs[0].Range.TotalBuckets != "603" {
		t.Fatal("comparison not bounded")
	}
	complete := call(t, handler, "POST", "/api/v1/export", `{"leftId":"paged","rightId":"other"}`, 200)
	if strings.Count(complete.Body.String(), `"index":"602"`) < 2 {
		t.Fatal("comparison export truncated")
	}
	t.Log("real SQLite scoped pages, stale 409 and complete export verified")
}
