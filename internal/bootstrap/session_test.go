package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionBoundary(t *testing.T) {
	s, err := NewSession("http://127.0.0.1:18080", "epoch", "owner-secret")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	address, err := s.LaunchURL()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", address, nil))
	if rr.Code != 303 || rr.Header().Get("Location") != "/" {
		t.Fatal(rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	replay := httptest.NewRecorder()
	h.ServeHTTP(replay, httptest.NewRequest("GET", address, nil))
	if replay.Code != 403 {
		t.Fatal("bootstrap replay accepted")
	}
	cases := []struct {
		name, method, host, origin, csrf string
		cookie                           bool
		want                             int
	}{
		{"anonymous", "GET", "127.0.0.1:18080", "", "", false, 401},
		{"read", "GET", "127.0.0.1:18080", "", "", true, 204},
		{"rebind", "GET", "evil.example", "", "", true, 403},
		{"foreign", "POST", "127.0.0.1:18080", "https://evil.example", s.csrf, true, 403},
		{"missing csrf", "POST", "127.0.0.1:18080", s.Origin, "", true, 403},
		{"missing origin", "POST", "127.0.0.1:18080", "", s.csrf, true, 403},
		{"write", "POST", "127.0.0.1:18080", s.Origin, s.csrf, true, 204},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, s.Origin+"/api/test", nil)
			r.Host = c.host
			r.Header.Set("Origin", c.origin)
			r.Header.Set("X-CSRF-Token", c.csrf)
			if c.cookie {
				r.AddCookie(cookies[0])
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.want {
				t.Fatalf("got %d want %d", w.Code, c.want)
			}
		})
	}
	t.Log("Single-use bootstrap and Host/Origin/CSRF rejection matrix passed")
}

func TestConfigPreservesUnsupported(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c.SchemaVersion != 1 || !c.OpenBrowser {
		t.Fatalf("%+v %v", c, err)
	}
	c.OpenBrowser = false
	if err = SaveConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(dir)
	if err != nil || c.OpenBrowser {
		t.Fatal(c, err)
	}
	path := filepath.Join(dir, "config.json")
	for _, raw := range []string{`{"schemaVersion":9}`, `{"schemaVersion":1,"unknown":true}`, `{"schemaVersion":1} {}`, `broken`} {
		if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = LoadConfig(dir); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		b, _ := os.ReadFile(path)
		if string(b) != raw {
			t.Fatal("config changed")
		}
	}
	t.Log("Defaults persisted; newer/corrupt configuration preserved")
}
