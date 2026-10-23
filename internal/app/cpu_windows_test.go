//go:build windows

package app

import (
	"highloader/internal/platform"
	"testing"
)

func TestSamplerIndependentAndJoined(t *testing.T) {
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sampler := NewSampler(func() platform.CPUValue { close(entered); <-release; x := 42.0; return platform.CPUValue{Percent: &x} }, func() error { close(closed); return nil })
	if sampler.Latest().Percent != nil || sampler.Latest().Reason == "" {
		t.Fatal("baseline should be missing")
	}
	await(t, entered)
	if sampler.Latest().Percent != nil {
		t.Fatal("native sample should still be blocked")
	}
	done := make(chan struct{})
	go func() { _ = sampler.Close(); close(done) }()
	select {
	case <-closed:
		t.Fatal("counter closed during sample")
	default:
	}
	close(release)
	await(t, done)
	await(t, closed)
	v := sampler.Latest()
	if v.Percent == nil || *v.Percent != 42 {
		t.Fatal(v)
	}
	if v.ID == "" || v.At.IsZero() || sampler.Latest().ID != v.ID || sampler.Latest().At != v.At {
		t.Fatal("sample identity/time changed across reads", v)
	}
	*v.Percent = 0
	if *sampler.Latest().Percent != 42 {
		t.Fatal("Latest aliases native value")
	}
	t.Log("Native work cannot block Latest; Close joins before releasing counter")
}
