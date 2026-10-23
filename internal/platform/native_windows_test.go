//go:build windows

package platform

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestNativeResultSemantics(t *testing.T) {
	if cancel, err := dialogStatus(0x800704C7); !cancel || err != nil {
		t.Fatal(cancel, err)
	}
	if cancel, err := dialogStatus(0); cancel || err != nil {
		t.Fatal(cancel, err)
	}
	if cancel, err := dialogStatus(0x80004005); cancel || err == nil {
		t.Fatal(cancel, err)
	}
	for _, value := range []float64{0, 50, 100} {
		sample := cpuValue(time.Now(), 0, 1, value)
		if sample.Percent == nil || *sample.Percent != value {
			t.Fatal(sample)
		}
	}
	for _, value := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		sample := cpuValue(time.Now(), 0, 0, value)
		if sample.Percent != nil || sample.MissingReason == "" {
			t.Fatal(sample)
		}
	}
	if sample := cpuValue(time.Now(), 0x800007D5, 0, 0); sample.Percent != nil || sample.MissingReason == "" {
		t.Fatal(sample)
	}
	if sample := cpuValue(time.Now(), 0, 0xC0000BC6, 0); sample.Percent != nil {
		t.Fatal(sample)
	}
	t.Log("Dialog cancellation distinct; invalid PDH samples remain gaps")
}

func TestPDHCapability(t *testing.T) {
	if os.Getenv("HIGHLOADER_PROBE_PDH") != "1" {
		t.Skip("Set HIGHLOADER_PROBE_PDH=1 for the explicit local-host capability probe")
	}
	cpu, err := OpenCPU()
	if err != nil {
		t.Logf("%v", err)
		return
	}
	defer cpu.Close()
	time.Sleep(time.Second)
	sample := cpu.Sample()
	if sample.Percent == nil {
		t.Logf("%s", sample.MissingReason)
		return
	}
	t.Logf("Local whole-host CPU %.3f%%", *sample.Percent)
	if err = cpu.Close(); err != nil {
		t.Fatal(err)
	}
	if cpu.Sample().Percent != nil {
		t.Fatal("closed counter yielded a value")
	}
}
