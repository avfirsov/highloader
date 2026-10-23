//go:build windows

package bootstrap

import (
	"context"
	"errors"
	"highloader/internal/platform"
	"log"
	"net/http"
	"testing"
)

func TestOwnedFactoryLifecycle(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	cleaned := false
	o := Options{DataDir: dir, DisableBrowser: true, BuildHandler: func(ctx context.Context, path string, l *log.Logger) (http.Handler, func(context.Context) error, error) {
		calls++
		if owner, e := platform.AcquireOwner(path); !errors.Is(e, platform.ErrAlreadyRunning) {
			if owner != nil {
				owner.Close()
			}
			t.Fatal("factory ran without exclusive ownership", e)
		}
		return http.NotFoundHandler(), func(context.Context) error {
			if owner, e := platform.AcquireOwner(path); !errors.Is(e, platform.ErrAlreadyRunning) {
				if owner != nil {
					owner.Close()
				}
				t.Error("shutdown released owner too early", e)
			}
			cleaned = true
			return nil
		}, nil
	}}
	r, e := Start(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close(context.Background())
	if _, e = Start(context.Background(), o); !errors.Is(e, ErrExistingInstance) || calls != 1 {
		t.Fatal("duplicate invoked factory", calls, e)
	}
	if e = r.Close(context.Background()); e != nil || !cleaned {
		t.Fatal("cleanup missing", e)
	}
	owner, e := platform.AcquireOwner(dir)
	if e != nil {
		t.Fatal(e)
	}
	owner.Close()
	t.Log("Factory and cleanup remain within ownership; duplicate cannot recover storage")
}

func TestOwnedFactoryRollback(t *testing.T) {
	dir := t.TempDir()
	cleaned := false
	_, e := Start(context.Background(), Options{DataDir: dir, DisableBrowser: true, BuildHandler: func(context.Context, string, *log.Logger) (http.Handler, func(context.Context) error, error) {
		return nil, func(context.Context) error {
			cleaned = true
			if owner, e := platform.AcquireOwner(dir); !errors.Is(e, platform.ErrAlreadyRunning) {
				if owner != nil {
					owner.Close()
				}
				t.Error("rollback after owner release")
			}
			return nil
		}, errors.New("construction failed")
	}})
	if e == nil || !cleaned {
		t.Fatal("failed factory missing cleanup", e)
	}
	owner, e := platform.AcquireOwner(dir)
	if e != nil {
		t.Fatal(e)
	}
	owner.Close()
	t.Log("Partial construction cleaned before lock release")
}

func TestOwnedShutdownFailure(t *testing.T) {
	dir := t.TempDir()
	r, e := Start(context.Background(), Options{DataDir: dir, DisableBrowser: true, BuildHandler: func(context.Context, string, *log.Logger) (http.Handler, func(context.Context) error, error) {
		return http.NotFoundHandler(), func(context.Context) error { return errors.New("cleanup failed") }, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer r.owner.Close()
	defer r.logFile.Close()
	if e = r.Close(context.Background()); e == nil {
		t.Fatal("cleanup error lost")
	}
	if owner, e := platform.AcquireOwner(dir); !errors.Is(e, platform.ErrAlreadyRunning) {
		if owner != nil {
			owner.Close()
		}
		t.Fatal("failure released ownership", e)
	}
	t.Log("Failed cleanup retains instance ownership")
}
