package bootstrap

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigWriteFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	prior := []byte(`{"schemaVersion":1,"openBrowser":false}`)
	if err := os.WriteFile(path, prior, 0600); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{SaveConfig(dir, Config{SchemaVersion: 2}), AtomicJSON(path, make(chan int))} {
		if err == nil {
			t.Fatal("invalid write accepted")
		}
		got, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(got, prior) {
			t.Fatal("prior data changed", e)
		}
	}
	if err := AtomicJSON(filepath.Join(path, "child"), 1); err == nil {
		t.Fatal("file used as parent")
	}
	target := filepath.Join(dir, "destination")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := AtomicJSON(target, 1); err == nil {
		t.Fatal("directory replaced")
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".highloader-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatal("temporary files leaked", temps, err)
	}
	t.Log("Invalid writes preserve old bytes and remove sibling temporary files")
}

func TestLogRotationBoundaries(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotate", true: "blocked"}[blocked], func(t *testing.T) {
			dir := t.TempDir()
			folder := filepath.Join(dir, "logs")
			if err := os.Mkdir(folder, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(folder, "highloader.log")
			prior := bytes.Repeat([]byte("x"), (4<<20)+1)
			if err := os.WriteFile(path, prior, 0600); err != nil {
				t.Fatal(err)
			}
			if blocked {
				if err := os.Mkdir(path+".1", 0700); err != nil {
					t.Fatal(err)
				}
			}
			logger, f, err := OpenLog(dir)
			if blocked {
				if err == nil {
					f.Close()
					t.Fatal("rotation failure hidden")
				}
				got, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(got, prior) {
					t.Fatal("old log lost", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			logger.Print("new diagnostic")
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			got, e := os.ReadFile(path + ".1")
			if e != nil || !bytes.Equal(got, prior) {
				t.Fatal("archive differs", e)
			}
			got, e = os.ReadFile(path)
			if e != nil || !bytes.Contains(got, []byte("new diagnostic")) {
				t.Fatal("new log missing", e)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, f, err := OpenLog(dir); err == nil {
		f.Close()
		t.Fatal("file accepted as log directory")
	}
	t.Log("Rotation retains old bytes; blocked destinations report errors")
}

func TestSessionExpiryAndCapacity(t *testing.T) {
	for _, origin := range []string{"https://127.0.0.1:80", "http://example.com:80", "http://127.0.0.1", "http://127.0.0.1:80/path", "%"} {
		if _, err := NewSession(origin, "epoch", "owner"); err == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
	s, err := NewSession("http://127.0.0.1:18080", "epoch", "owner")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("guard leaked request") }))
	address, err := s.LaunchURL()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	s.tokens[u.Query().Get("token")] = time.Now().Add(-time.Hour)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", address, nil))
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", address, nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	for i := 0; i < 32; i++ {
		if _, err = s.LaunchURL(); err != nil {
			t.Fatal(i, err)
		}
	}
	r := httptest.NewRequest("POST", s.Origin+"/internal/reopen", nil)
	r.Header.Set("X-Highloader-Owner", s.Secret)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
	for token := range s.tokens {
		s.tokens[token] = time.Now().Add(-time.Hour)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	r.Header.Set("Origin", s.Origin)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	t.Log("Expired capabilities rejected; exhausted reopen returns 429; expiration restores capacity")
}
