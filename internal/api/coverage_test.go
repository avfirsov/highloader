package api

import (
	"context"
	"errors"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/history"
	"highloader/internal/report"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectedRoutes(t *testing.T) {
	cases := []struct{ method, path, body string }{
		{"PUT", "/api/v1/draft", `{"expectedRevision":"01","settings":{}}`},
		{"POST", "/api/v1/runs", `{"expectedRevision":"-1","idempotencyKey":"k"}`},
		{"POST", "/api/v1/runs", `{`},
		{"POST", "/api/v1/import-folder", `null`},
		{"POST", "/api/v1/import-folder", `{"expectedRevision":"18446744073709551616"}`},
		{"POST", "/api/v1/runs/r/repeat", `{"expectedRevision":""}`},
		{"POST", "/api/v1/runs/r/repeat", `{`},
		{"POST", "/api/v1/draft/validate", `{"unexpected":true}`},
		{"POST", "/api/v1/runs/r/stop", `[]`},
		{"POST", "/api/v1/runs/r/force-stop", `{} {}`},
		{"DELETE", "/api/v1/runs/r", `null`},
		{"DELETE", "/api/v1/history", `{"id":"r"}`},
		{"POST", "/api/v1/exit", `{"force":true}`},
		{"POST", "/api/v1/exit/discard", `null`},
		{"GET", "/api/v1/history?limit=1&limit=2", ""},
		{"GET", "/api/v1/history?offset=-1", ""},
		{"GET", "/api/v1/runs/bad.id/view", ""},
		{"GET", "/api/v1/runs/bad.id/live", ""},
		{"GET", "/api/v1/runs/bad.id/report", ""},
		{"POST", "/api/v1/runs/bad.id/repeat", `{}`},
		{"POST", "/api/v1/compare", `{}`},
		{"POST", "/api/v1/compare", `null`},
		{"POST", "/api/v1/export", `{"runId":"r","leftId":"a"}`},
		{"POST", "/api/v1/export", `[]`},
	}
	for _, query := range []string{"limit=0", "sampleOffset=-1", "sampleLimit=2001", "fromIndex=01", "revision=01", "population=invalid", "templateId=bad.id", "unknown=1", "limit=1&limit=2"} {
		cases = append(cases, struct{ method, path, body string }{"GET", "/api/v1/runs/r/report?" + query, ""})
	}
	h := Handler(nil, nil)
	for i, c := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if w.Code != 422 || !strings.Contains(w.Body.String(), `"code":"invalid_input"`) {
				t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body)
			}
		})
	}
	t.Log("invalid routes rejected before any service call")
}

func TestSafeResponses(t *testing.T) {
	for _, c := range []struct {
		err    error
		code   string
		status int
	}{
		{history.ErrNotFound, "not_found", 404}, {history.ErrActive, "active", 409}, {history.ErrStale, "stale_report", 409},
		{errors.New("private SQL sentinel"), "unavailable", 503}, {&Error{Code: "reserved", Message: "busy", Status: 409}, "reserved", 409},
	} {
		t.Run(c.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			respond(w, nil, fmt.Errorf("private wrapper: %w", c.err))
			if w.Code != c.status || !strings.Contains(w.Body.String(), `"code":"`+c.code+`"`) || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
	w := httptest.NewRecorder()
	respond(w, make(chan int), nil)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	e := &Error{Message: "visible"}
	if e.Error() != "visible" {
		t.Fatal(e.Error())
	}
	h := Handler(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
	for path, want := range map[string]int{"/api/unknown": 404, "/index.html": 202} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatal(path, w.Code)
		}
	}
	t.Log("typed errors mapped; raw diagnostics and marshal failures sanitized")
}

type evidenceService struct {
	Service
	options   report.Options
	selection Selection
	err       error
}

func (s *evidenceService) Report(_ context.Context, id string, o report.Options) (report.RunReport, error) {
	s.options = o
	return report.RunReport{ID: id}, s.err
}
func (s *evidenceService) Export(_ context.Context, v Selection) ([]byte, error) {
	s.selection = v
	return []byte("<html>evidence</html>"), s.err
}

func TestEvidenceDispatch(t *testing.T) {
	s := &evidenceService{}
	h := Handler(s, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/runs/r/report?population=http&templateId=t&fromIndex=9&limit=2&sampleOffset=3&sampleLimit=4&revision=18446744073709551615", nil))
	o := s.options
	if w.Code != 200 || o.TemplateID != "t" || o.Population != "http" || o.FromIndex != 9 || o.Limit != 2 || o.SampleOffset != 3 || o.SampleLimit != 4 || o.ExpectedSequence == nil || *o.ExpectedSequence != ^uint64(0) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, o, w.Header())
	}
	for _, failExport := range []bool{false, true} {
		if failExport {
			s.err = history.ErrNotFound
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/export", strings.NewReader(`{"runId":"r","includeAuthorization":true}`)))
		if failExport {
			if w.Code != 404 {
				t.Fatal(w.Code)
			}
		} else if w.Code != 200 || !s.selection.IncludeAuthorization || w.Body.String() != "<html>evidence</html>" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Disposition"), "highloader-report.html") {
			t.Fatal(w.Code, w.Header(), w.Body)
		}
	}
	t.Log("scoped pinned report and explicit export options preserved")
}

type mutationService struct {
	Service
	calls    []string
	revision uint64
}

func (s *mutationService) Repeat(_ context.Context, id string, rev uint64) (domain.Draft, error) {
	s.calls = append(s.calls, "repeat:"+id)
	s.revision = rev
	return domain.NewDraft(), nil
}
func (s *mutationService) Delete(_ context.Context, id string) error {
	s.calls = append(s.calls, "delete:"+id)
	return nil
}
func (s *mutationService) Clear(context.Context) error {
	s.calls = append(s.calls, "clear")
	return nil
}
func (s *mutationService) DiscardAndExit() error { s.calls = append(s.calls, "discard"); return nil }

func TestMutationDispatch(t *testing.T) {
	for _, c := range []struct{ method, path, body, want string }{
		{"POST", "/api/v1/runs/saved/repeat", `{"expectedRevision":"23"}`, "repeat:saved"},
		{"DELETE", "/api/v1/runs/saved", "", "delete:saved"},
		{"DELETE", "/api/v1/history", "{}", "clear"},
		{"POST", "/api/v1/exit/discard", "", "discard"},
	} {
		t.Run(c.want, func(t *testing.T) {
			s := &mutationService{}
			w := httptest.NewRecorder()
			Handler(s, nil).ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if w.Code != 200 || len(s.calls) != 1 || s.calls[0] != c.want {
				t.Fatal(w.Code, s.calls, w.Body)
			}
			if c.want == "repeat:saved" && s.revision != 23 {
				t.Fatal(s.revision)
			}
		})
	}
	t.Log("repeat, delete, clear and discard reach exactly one intended service operation")
}
