package integration

import (
	"context"
	"encoding/json"
	"highloader/internal/api"
	"highloader/internal/app"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func apiFixture(t *testing.T) (*app.Application, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "source.json"), []byte(`{"large":9007199254740993}`), 0600); e != nil {
		t.Fatal(e)
	}
	store, e := history.Open(filepath.Join(dir, "history.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := app.New(app.Options{Store: store, PickFolder: func() (string, bool, error) { return dir, false, nil }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ctx, c := context.WithTimeout(context.Background(), time.Second); defer c(); _ = a.Close(ctx) })
	return a, api.Handler(a, nil)
}
func call(t *testing.T, h http.Handler, method, path, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
	}
	return w
}

func TestApplicationAPIWorkflow(t *testing.T) {
	a, h := apiFixture(t)
	w := call(t, h, "GET", "/api/v1/draft", "", 200)
	if strings.Contains(w.Body.String(), `"templates":null`) {
		t.Fatal("empty arrays serialized as null")
	}
	call(t, h, "POST", "/api/v1/import-folder", `{"expectedRevision":"0"}`, 200)
	d := a.Draft()
	d.URL = "http://127.0.0.1:1"
	d.Profile = domain.Profile{Mode: "closed", Steps: []domain.Step{{DurationSeconds: 60, Level: 1}}, RetryAccounting: "within"}
	d.Capacity = 1
	raw, _ := json.Marshal(d)
	var editor api.EditableDraft
	_ = json.Unmarshal(raw, &editor)
	body, _ := json.Marshal(struct {
		ExpectedRevision string            `json:"expectedRevision"`
		Settings         api.EditableDraft `json:"settings"`
	}{"1", editor})
	call(t, h, "PUT", "/api/v1/draft", string(body), 200)
	call(t, h, "POST", "/api/v1/draft/validate", `{}`, 200)
	w = call(t, h, "POST", "/api/v1/runs", `{"expectedRevision":"2","idempotencyKey":"api-once"}`, 200)
	var active api.Active
	if e := json.Unmarshal(w.Body.Bytes(), &active); e != nil || active.RunID == nil {
		t.Fatal(e)
	}
	id := *active.RunID
	call(t, h, "POST", "/api/v1/runs", `{"expectedRevision":"0","idempotencyKey":"api-once"}`, 200)
	w = call(t, h, "POST", "/api/v1/runs/"+id+"/force-stop", `{}`, 200)
	var view engine.View
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if view.RunID != id {
		t.Fatal("stop response must be bare view")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, _ = a.View(context.Background(), id)
		if view.WorkersReleased && view.Durable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finalization timeout")
		}
		time.Sleep(time.Millisecond)
	}
	call(t, h, "GET", "/api/v1/active", "", 200)
	call(t, h, "GET", "/api/v1/runs/"+id+"/view", "", 200)
	call(t, h, "GET", "/api/v1/runs/"+id+"/report?population=all&limit=600", "", 200)
	call(t, h, "GET", "/api/v1/history?limit=1&offset=0", "", 200)
	w = call(t, h, "POST", "/api/v1/export", `{"runId":"`+id+`"}`, 503)
	if !strings.Contains(w.Body.String(), "report_assets_missing") {
		t.Fatal("missing build not actionable")
	}
	// App release follows engine publication; retry the mutation only after app cleanup joins.
	if e := a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Log("Real API saved, started once, forced, reconnected and reported; missing offline build surfaced explicitly")
}

func TestApplicationAPIStrictInputs(t *testing.T) {
	_, h := apiFixture(t)
	for _, b := range []string{`{"expectedRevision":0,"settings":{}}`, `{"expectedRevision":"00","settings":{}}`, `{"expectedRevision":"0","settings":{"templates":[]}}`, `{"expectedRevision":"0","settings":{"raw":"e30="}}`, `null`, `{} {}`} {
		call(t, h, "PUT", "/api/v1/draft", b, 422)
	}
	for _, q := range []string{"limit=0", "limit=2001", "fromIndex=-1", "fromIndex=1.2", "population=wrong", "sampleLimit=0", "templateId=x/y", "limit=1&limit=2", "unknown=x"} {
		call(t, h, "GET", "/api/v1/runs/id/report?"+q, "", 422)
	}
	call(t, h, "GET", "/api/v1/history?offset=-1", "", 422)
	for _, b := range []string{`{}`, `{"runId":"a","leftId":"b"}`, `{"leftId":"a","rightId":"a"}`, `{"leftId":"a","rightId":"b","mapping":{"x":"z","y":"z"}}`} {
		call(t, h, "POST", "/api/v1/export", b, 422)
	}
	call(t, h, "POST", "/api/v1/compare", `{"leftId":"missing","rightId":"other"}`, 404)
	call(t, h, "POST", "/api/v1/draft/validate", `{"templates":[]}`, 422)
	call(t, h, "GET", "/api/v1/unknown", "", 404)
	t.Log("Protected fields, nondecimal revisions, invalid pages and ambiguous mappings rejected")
}
