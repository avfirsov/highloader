//go:build windows

package bootstrap

import (
	"context"
	"highloader/internal/platform"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStartupFailureReleasesOwner(t *testing.T) {
	for _, kind := range []string{"config", "logs", "descriptor", "nil-handler", "nil-cleanup"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			cleaned := false
			built := false
			switch kind {
			case "config":
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "logs":
				if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				if err := os.Mkdir(filepath.Join(dir, "owner.json"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			runtime, err := Start(context.Background(), Options{DataDir: dir, DisableBrowser: true, BuildHandler: func(context.Context, string, *log.Logger) (http.Handler, func(context.Context) error, error) {
				built = true
				var h http.Handler = http.NotFoundHandler()
				var cleanup func(context.Context) error = func(context.Context) error { cleaned = true; return nil }
				if kind == "nil-handler" {
					h = nil
				}
				if kind == "nil-cleanup" {
					cleanup = nil
				}
				return h, cleanup, nil
			}})
			if err == nil {
				runtime.Close(context.Background())
				t.Fatal("startup unexpectedly succeeded")
			}
			wantBuilt := kind != "config" && kind != "logs"
			if built != wantBuilt || cleaned != (wantBuilt && kind != "nil-cleanup") {
				t.Fatal("factory cleanup mismatch", built, cleaned)
			}
			owner, e := platform.AcquireOwner(dir)
			if e != nil {
				t.Fatal("owner retained", e)
			}
			if e = owner.Close(); e != nil {
				t.Fatal(e)
			}
		})
	}
	t.Log("Config/log/publication/factory failures clean up and release ownership")
}
