//go:build windows

package platform

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestOwnerExclusive(t *testing.T) {
	dir := t.TempDir()
	a, err := AcquireOwner(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := AcquireOwner(filepath.Join(dir, "."))
	if b != nil {
		b.Close()
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate error=%v", err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = AcquireOwner(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	t.Log("Duplicate alias rejected; ownership reusable after release")
}
