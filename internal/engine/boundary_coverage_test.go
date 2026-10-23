package engine

import (
	"encoding/json"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/metrics"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestCompletionIntegrity(t *testing.T) {
	// Valid populations -> incompatible additions -> unchanged serialized evidence.
	var base CompletionValue
	base.Attempts[0] = 1
	if err := base.AttemptDurations[0].Add(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	base.Logical[0] = 1
	if err := base.LogicalDurations[0].Add(2 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	cases := []CompletionValue{{Attempts: [6]Counter{1}}, {Logical: [5]Counter{1}}, {Attempts: [6]Counter{0, 0, 0, 0, math.MaxUint64, 1}}, {Logical: [5]Counter{0, 0, 0, math.MaxUint64, 1}}}
	for i, bad := range cases {
		before, _ := json.Marshal(base)
		if err := base.Merge(bad); err == nil {
			t.Fatalf("case %d accepted", i)
		}
		after, _ := json.Marshal(base)
		if string(before) != string(after) {
			t.Fatalf("case %d mutated accepted evidence", i)
		}
	}
	for _, logical := range []bool{false, true} {
		var full, extra CompletionValue
		if logical {
			full.Logical[3] = math.MaxUint64
			extra.Logical[3] = 1
		} else {
			full.Attempts[4] = math.MaxUint64
			extra.Attempts[4] = 1
		}
		before, _ := json.Marshal(full)
		if err := full.Merge(extra); !errors.Is(err, metrics.ErrOverflow) {
			t.Fatalf("overflow: %v", err)
		}
		after, _ := json.Marshal(full)
		if string(before) != string(after) {
			t.Fatal("overflow mutated receiver")
		}
	}
	if err := base.Merge(base); err != nil {
		t.Fatal(err)
	}
	d, err := base.AttemptDistribution(metrics.DefaultPopulation)
	if err != nil || d.Summary().Count != 2 || base.Logical[0] != 2 {
		t.Fatal("valid merge lost population")
	}
	if _, err = base.AttemptDistribution(metrics.Population(128)); !errors.Is(err, metrics.ErrInvalid) {
		t.Fatal(err)
	}
	t.Log("Invalid populations and overflow rejected atomically; valid evidence doubles exactly")
}

func TestWireRejection(t *testing.T) {
	// Malformed wire -> error -> prior receiver survives.
	for _, raw := range []string{`1`, `"-1"`, `"18446744073709551616"`, `"abc"`, `null`} {
		c := Counter(17)
		if err := json.Unmarshal([]byte(raw), &c); err == nil || c != 17 {
			t.Fatalf("counter %s: %d %v", raw, c, err)
		}
	}
	for _, raw := range []string{`{"Sequence":1}`, `{"Attempt":"-1"}`, `{"Duration":"9223372036854775808"}`, `{"Request":{"Total":1}}`, `{`} {
		s := Sample{Sequence: 17, Duration: time.Second}
		before := s
		if err := json.Unmarshal([]byte(raw), &s); err == nil || !reflect.DeepEqual(s, before) {
			t.Fatalf("sample %s mutated or accepted: %+v %v", raw, s, err)
		}
	}
	t.Log("Malformed numeric wire fields rejected without receiver mutation")
}

func TestCPUValidity(t *testing.T) {
	// Explicit acquisition times -> valid/missing statistics -> detached values.
	r, c, _ := controlled(t, fixture())
	source := &cpuFixture{}
	r.options.CPU = source
	r.sampleCPU()
	if r.cpuDiagnostic == "" || r.cpuStats.Missing != 0 {
		t.Fatal("unidentified sample counted")
	}
	c.set(500 * time.Millisecond)
	r.advance(c.Now())
	percent := 50.0
	for _, at := range []time.Time{r.origin.Add(-time.Second), c.Now().Add(time.Second)} {
		source.value = CPUValue{ID: "outside", At: at, Percent: &percent}
		r.sampleCPU()
	}
	if r.cpuStats.Valid != 0 {
		t.Fatal("out-of-window sample counted")
	}
	for i, value := range []float64{math.NaN(), math.Inf(1), -1, 101} {
		source.value = CPUValue{ID: string(rune('a' + i)), At: r.origin.Add(time.Duration(i+1) * time.Millisecond), Percent: &value}
		r.sampleCPU()
		if r.lastCPU.Percent != nil || r.lastCPU.Reason == "" {
			t.Fatal("invalid CPU retained as finite value")
		}
	}
	if r.cpuStats.Missing != 4 {
		t.Fatal(r.cpuStats)
	}
	source.value = CPUValue{ID: "valid", At: c.Now(), Percent: &percent}
	r.sampleCPU()
	percent = 99
	if r.cpuStats.Valid != 1 || r.cpuStats.Sum != 50 || *r.lastCPU.Percent != 50 {
		t.Fatal("CPU evidence aliases mutable provider")
	}
	t.Log("Out-of-window samples ignored; invalid CPU counted as missing; accepted value detached")
}

func TestPendingCompletionReport(t *testing.T) {
	// Event-time completions -> report copy -> later events cannot change old report.
	r, _, _ := controlled(t, fixture())
	outcome := metrics.Success
	for _, event := range []struct {
		id string
		at time.Duration
	}{{"z", 2 * time.Second}, {"a", time.Second}, {"z", time.Second}} {
		r.recordCompletion(event.id, r.origin.Add(event.at), &outcome, nil, time.Millisecond)
	}
	old := r.unflushedCompletion()
	if len(old) != 5 {
		t.Fatal(len(old))
	}
	for i := 1; i < len(old); i++ {
		a, b := old[i-1], old[i]
		if a.TemplateID > b.TemplateID || a.TemplateID == b.TemplateID && a.Index >= b.Index {
			t.Fatal("unordered pending report")
		}
	}
	before, _ := json.Marshal(old)
	r.recordCompletion("z", r.origin.Add(time.Second), &outcome, nil, 2*time.Millisecond)
	after, _ := json.Marshal(old)
	if string(before) != string(after) {
		t.Fatal("pending report changed after subsequent event")
	}
	r.flushCompletion(false)
	if len(r.unflushedCompletion()) != 0 {
		t.Fatal("flushed evidence repeated as pending")
	}
	t.Log("Pending report ordered and detached; flush removes pending ownership")
}

func TestConstructorGuards(t *testing.T) {
	// Invalid serialized input -> constructor error -> no runner.
	owner, _, _ := controlled(t, fixture())
	for _, mutate := range []func(*domain.RunSnapshot){
		func(s *domain.RunSnapshot) { s.RunID = "" },
		func(s *domain.RunSnapshot) { s.Effective = nil },
		func(s *domain.RunSnapshot) { s.Draft.Samples.Success = -1 },
		func(s *domain.RunSnapshot) { s.Draft.Templates[0].Body = []byte(`"{{bad}}"`) },
		func(s *domain.RunSnapshot) { s.Effective[0].Weight = math.NaN() },
	} {
		s := fixture()
		mutate(&s)
		if r, err := New(s, Options{Transport: owner.options.Transport}); err == nil || r != nil {
			t.Fatal("invalid snapshot admitted")
		}
	}
	t.Log("Invalid configuration, quota, tokens and nonfinite wire data rejected before runner creation")
}
