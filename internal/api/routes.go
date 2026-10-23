package api

import (
	"encoding/json"
	"errors"
	"highloader/internal/history"
	"highloader/internal/report"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func Handler(s Service, static http.Handler) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/draft", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Draft(), nil) })
	m.HandleFunc("PUT /api/v1/draft", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ExpectedRevision string        `json:"expectedRevision"`
			Settings         EditableDraft `json:"settings"`
		}
		if !decode(w, r, &b) {
			return
		}
		rev, e := revision(b.ExpectedRevision)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.Update(rev, b.Settings)
		respond(w, v, e)
	})
	m.HandleFunc("POST /api/v1/draft/validate", func(w http.ResponseWriter, r *http.Request) {
		if !empty(w, r) {
			return
		}
		respond(w, s.Validate(), nil)
	})
	m.HandleFunc("POST /api/v1/import-folder", func(w http.ResponseWriter, r *http.Request) {
		rev, ok := readRevision(w, r)
		if !ok {
			return
		}
		v, e := s.Import(r.Context(), rev)
		respond(w, v, e)
	})
	m.HandleFunc("POST /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ExpectedRevision string `json:"expectedRevision"`
			IdempotencyKey   string `json:"idempotencyKey"`
		}
		if !decode(w, r, &b) {
			return
		}
		rev, e := revision(b.ExpectedRevision)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.Start(r.Context(), rev, b.IdempotencyKey)
		respond(w, v, e)
	})
	m.HandleFunc("GET /api/v1/active", func(w http.ResponseWriter, r *http.Request) { respond(w, s.Active(), nil) })
	for _, suffix := range []string{"view", "live"} {
		m.HandleFunc("GET /api/v1/runs/{id}/"+suffix, func(w http.ResponseWriter, r *http.Request) {
			if !validPath(w, r) {
				return
			}
			v, e := s.View(r.Context(), r.PathValue("id"))
			respond(w, v, e)
		})
	}
	for _, force := range []bool{false, true} {
		suffix := "stop"
		if force {
			suffix = "force-stop"
		}
		m.HandleFunc("POST /api/v1/runs/{id}/"+suffix, func(w http.ResponseWriter, r *http.Request) {
			if !validPath(w, r) || !empty(w, r) {
				return
			}
			v, e := s.Stop(r.Context(), r.PathValue("id"), force)
			respond(w, v, e)
		})
	}
	m.HandleFunc("GET /api/v1/history", func(w http.ResponseWriter, r *http.Request) {
		limit, e := integer(r, "limit", 50, 1, 1000)
		if e != nil {
			fail(w, e)
			return
		}
		offset, e := integer(r, "offset", 0, 0, int(^uint(0)>>1)-1000)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.History(r.Context(), limit, offset)
		respond(w, v, e)
	})
	m.HandleFunc("POST /api/v1/runs/{id}/repeat", func(w http.ResponseWriter, r *http.Request) {
		if !validPath(w, r) {
			return
		}
		rev, ok := readRevision(w, r)
		if !ok {
			return
		}
		v, e := s.Repeat(r.Context(), r.PathValue("id"), rev)
		respond(w, v, e)
	})
	m.HandleFunc("DELETE /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !validPath(w, r) || !empty(w, r) {
			return
		}
		respond(w, struct{}{}, s.Delete(r.Context(), r.PathValue("id")))
	})
	m.HandleFunc("DELETE /api/v1/history", func(w http.ResponseWriter, r *http.Request) {
		if !empty(w, r) {
			return
		}
		respond(w, struct{}{}, s.Clear(r.Context()))
	})
	registerEvidence(m, s)
	m.HandleFunc("POST /api/v1/exit/discard", func(w http.ResponseWriter, r *http.Request) {
		if !empty(w, r) {
			return
		}
		respond(w, struct{}{}, s.DiscardAndExit())
	})
	m.HandleFunc("POST /api/v1/exit", func(w http.ResponseWriter, r *http.Request) {
		if !empty(w, r) {
			return
		}
		s.Exit()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("{}"))
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, &Error{Code: "not_found", Message: "Операция не найдена.", Status: 404})
	})
	if static != nil {
		m.Handle("/", static)
	}
	return m
}

func registerEvidence(m *http.ServeMux, s Service) {
	m.HandleFunc("GET /api/v1/runs/{id}/report", func(w http.ResponseWriter, r *http.Request) {
		if !validPath(w, r) {
			return
		}
		o, e := reportOptions(r)
		if e != nil {
			fail(w, e)
			return
		}
		v, e := s.Report(r.Context(), r.PathValue("id"), o)
		respond(w, v, e)
	})
	m.HandleFunc("POST /api/v1/compare", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			LeftID  string            `json:"leftId"`
			RightID string            `json:"rightId"`
			Mapping map[string]string `json:"mapping,omitempty"`
		}
		if !decode(w, r, &b) {
			return
		}
		if e := selection(Selection{LeftID: b.LeftID, RightID: b.RightID, Mapping: b.Mapping}); e != nil {
			fail(w, e)
			return
		}
		v, e := s.Compare(r.Context(), b.LeftID, b.RightID, b.Mapping)
		respond(w, v, e)
	})
	m.HandleFunc("POST /api/v1/export", func(w http.ResponseWriter, r *http.Request) {
		var b Selection
		if !decode(w, r, &b) {
			return
		}
		if e := selection(b); e != nil {
			fail(w, e)
			return
		}
		data, e := s.Export(r.Context(), b)
		if e != nil {
			fail(w, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="highloader-report.html"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	var raw json.RawMessage
	if err := d.Decode(&raw); err != nil || string(raw) == "null" {
		fail(w, invalid())
		return false
	}
	strict := json.NewDecoder(strings.NewReader(string(raw)))
	strict.DisallowUnknownFields()
	if err := strict.Decode(dst); err != nil {
		fail(w, invalid())
		return false
	}
	var more any
	if err := d.Decode(&more); err != io.EOF {
		fail(w, invalid())
		return false
	}
	return true
}
func empty(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	return decode(w, r, &struct{}{})
}
func readRevision(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	var b struct {
		ExpectedRevision string `json:"expectedRevision"`
	}
	if !decode(w, r, &b) {
		return 0, false
	}
	v, e := revision(b.ExpectedRevision)
	if e != nil {
		fail(w, e)
		return 0, false
	}
	return v, true
}
func revision(s string) (uint64, error) {
	v, e := strconv.ParseUint(s, 10, 64)
	if e != nil || strconv.FormatUint(v, 10) != s {
		return 0, invalid()
	}
	return v, nil
}
func invalid() error {
	return &Error{Code: "invalid_input", Message: "Некорректные параметры запроса.", Status: 422}
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
func fail(w http.ResponseWriter, err error) {
	e := &Error{Code: "unavailable", Message: "Операция недоступна. Проверьте состояние приложения и хранилища.", Status: 503}
	var known *Error
	switch {
	case errors.As(err, &known):
		e = known
	case errors.Is(err, history.ErrNotFound):
		e = &Error{Code: "not_found", Message: "Запуск не найден.", Status: 404}
	case errors.Is(err, history.ErrActive):
		e = &Error{Code: "active", Message: "Есть незавершённый запуск.", Status: 409}
	case errors.Is(err, history.ErrStale):
		e = &Error{Code: "stale_report", Message: "Срез отчёта изменился. Обновите страницу отчёта.", Status: 409}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

func validID(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validPath(w http.ResponseWriter, r *http.Request) bool {
	if !validID(r.PathValue("id")) {
		fail(w, invalid())
		return false
	}
	return true
}
func integer(r *http.Request, key string, fallback, min, max int) (int, error) {
	values, ok := r.URL.Query()[key]
	if !ok {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, invalid()
	}
	n, e := strconv.Atoi(values[0])
	if e != nil || n < min || n > max || strconv.Itoa(n) != values[0] {
		return 0, invalid()
	}
	return n, nil
}
func selection(s Selection) error {
	if s.RunID != "" {
		if !validID(s.RunID) || s.LeftID != "" || s.RightID != "" || len(s.Mapping) != 0 {
			return invalid()
		}
		return nil
	}
	if !validID(s.LeftID) || !validID(s.RightID) || s.LeftID == s.RightID {
		return invalid()
	}
	seen := map[string]bool{}
	for k, v := range s.Mapping {
		if !validID(k) || !validID(v) || seen[v] {
			return invalid()
		}
		seen[v] = true
	}
	return nil
}
func reportOptions(r *http.Request) (report.Options, error) {
	o := report.Options{Population: r.URL.Query().Get("population"), TemplateID: r.URL.Query().Get("templateId")}
	for k, v := range r.URL.Query() {
		if len(v) != 1 {
			return o, invalid()
		}
		switch k {
		case "population", "templateId", "fromIndex", "limit", "sampleOffset", "sampleLimit", "revision":
		default:
			return o, invalid()
		}
	}
	if o.Population != "" && o.Population != "all" && o.Population != "success" && o.Population != "http" && o.Population != "technical" {
		return o, invalid()
	}
	if o.TemplateID != "" && !validID(o.TemplateID) {
		return o, invalid()
	}
	from := r.URL.Query().Get("fromIndex")
	if from != "" {
		n, e := strconv.ParseInt(from, 10, 64)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != from {
			return o, invalid()
		}
		o.FromIndex = n
	}
	var e error
	if raw := r.URL.Query().Get("revision"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || strconv.FormatUint(v, 10) != raw {
			return o, invalid()
		}
		o.ExpectedSequence = &v
	}
	o.Limit, e = integer(r, "limit", 600, 1, 2000)
	if e != nil {
		return o, e
	}
	o.SampleOffset, e = integer(r, "sampleOffset", 0, 0, int(^uint(0)>>1)-2000)
	if e != nil {
		return o, e
	}
	o.SampleLimit, e = integer(r, "sampleLimit", 100, 1, 2000)
	return o, e
}
