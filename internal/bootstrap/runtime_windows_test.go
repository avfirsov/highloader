//go:build windows

package bootstrap

import (
	"context"
	"errors"
	"highloader/internal/platform"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "готово") })
	app, err := Start(ctx, Options{DataDir: dir, Handler: h, DisableBrowser: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(ctx)
	if !strings.HasPrefix(app.Origin, "http://127.0.0.1:") {
		t.Fatal(app.Origin)
	}
	address, err := Reopen(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	defer client.CloseIdleConnections()
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != "готово" {
		t.Fatal(response.StatusCode, string(body))
	}
	duplicate, err := Start(ctx, Options{DataDir: dir, Handler: h, DisableBrowser: true})
	if duplicate != nil || !errors.Is(err, ErrExistingInstance) {
		t.Fatalf("duplicate=%v err=%v", duplicate, err)
	}
	if err = app.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = app.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "owner.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descriptor remains", err)
	}
	owner, err := platform.AcquireOwner(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner.Close()
	b, err := os.ReadFile(filepath.Join(dir, "logs", "highloader.log"))
	if err != nil || !strings.Contains(string(b), "Loopback server ready at ") || !strings.Contains(string(b), "Loopback server stopped; ownership released") {
		t.Fatal(string(b), err)
	}
	t.Log(string(b))
	t.Log("One live owner; duplicate reopened; authenticated UI survived independent launch; cleanup released lock")
}

func TestBrowserFailure(t *testing.T) {
	var message string
	app, err := Start(context.Background(), Options{DataDir: t.TempDir(), Handler: http.NotFoundHandler(), Browser: func(string) error { return errors.New("association missing") }, Message: func(title, body string) error { message = body; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	if !strings.Contains(message, app.Origin+"/bootstrap?token=") {
		t.Fatal("fallback lacks usable link", message)
	}
	t.Log("Failed browser association retained running server and actionable fallback")
}
