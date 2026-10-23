package history

import (
	"context"
	"errors"
	"testing"
)

func TestFindStartAfterRestart(t *testing.T) {
	s, path := openTest(t)
	ctx := context.Background()
	if _, e := s.FindStart(ctx, "missing"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e := s.Start(ctx, fixture("original"), "key"); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	result, e := s.FindStart(ctx, "key")
	if e != nil || !result.Existing || result.RunID != "original" {
		t.Fatal(result, e)
	}
	if _, e = s.FindStart(ctx, "KEY"); !errors.Is(e, ErrNotFound) {
		t.Fatal("key lookup was not exact", e)
	}
	t.Log("Persisted exact key survives restart without draft validation")
}
