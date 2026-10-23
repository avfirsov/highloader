//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopArgumentRejection(t *testing.T) {
	for _, address := range []string{"%", "https://127.0.0.1:80", "http://example.com", "file:///tmp/test", "http://127.0.0.1:80/\x00"} {
		if err := OpenBrowser(address); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	if err := ShowMessage("bad\x00title", "body"); err == nil {
		t.Fatal("invalid title accepted")
	}
	if err := ShowMessage("title", "bad\x00body"); err == nil {
		t.Fatal("invalid message accepted")
	}
	t.Log("Invalid desktop inputs return before native UI")
}

func TestDataDirectoryAndOwnerFailure(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")
	if path, err := DefaultDataDir(); err == nil || path != "" {
		t.Fatal(path, err)
	}
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	path, err := DefaultDataDir()
	if err != nil || path != filepath.Join(root, "Highloader") {
		t.Fatal(path, err)
	}
	blocked := filepath.Join(root, "file")
	if err = os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if owner, e := AcquireOwner(blocked); e == nil {
		owner.Close()
		t.Fatal("file directory accepted")
	}
	dir := filepath.Join(root, "data")
	if err = os.MkdirAll(filepath.Join(dir, "owner.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if owner, e := AcquireOwner(dir); e == nil {
		owner.Close()
		t.Fatal("directory lock accepted")
	}
	t.Log("User data path mapped; unusable owner storage rejected")
}
