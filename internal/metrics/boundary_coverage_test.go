package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInvalidObservationAtomicity(t *testing.T) {
	// Reserved admission -> rejection -> unchanged evidence -> valid terminal observation.
	for _, o := range []Observation{{Status: 99}, {Status: 1000}, {Status: 200, Duration: -1}, {Status: 503, BodyReadFailure: true, Duration: -1}} {
		var a Aggregate
		if err := a.Admit(false); err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(a)
		if err := a.Observe(o); err != ErrInvalid {
			t.Fatalf("invalid observation: %v", err)
		}
		after, _ := json.Marshal(a)
		if string(before) != string(after) {
			t.Fatal("rejected observation mutated accounting")
		}
		if err := a.Observe(Observation{Status: 200, Duration: time.Millisecond}); err != nil {
			t.Fatal(err)
		}
		if a.Attempts[Success] != 1 || a.Statuses[200] != 1 {
			t.Fatal("admission not reusable")
		}
	}
	var a Aggregate
	if err := a.StartLogical(); err != nil {
		t.Fatal(err)
	}
	if err := a.FinishLogical(LogicalSuccess, -1); err != ErrInvalid {
		t.Fatal(err)
	}
	if err := a.FinishLogical(LogicalSuccess, time.Second); err != nil {
		t.Fatal(err)
	}
	if a.Logical[LogicalSuccess] != 1 || a.LogicalDurations[LogicalSuccess].Summary().Count != 1 {
		t.Fatal("logical capacity consumed by invalid completion")
	}
	t.Log("Failed observations preserve accounting and reserved admissions")
}
