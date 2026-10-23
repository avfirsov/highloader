package report

import (
	"encoding/base64"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/metrics"
	"strings"
	"testing"
	"time"
)

func fixture() history.Report {
	cut := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	a := engine.Aggregate{Initial: 3, Retries: 1, Started: 3, Statuses: map[string]string{"503": "2", "200": "1"}, Attempts: []string{"1", "1", "0", "1", "1", "0"}, Logical: []string{"1", "1", "0", "1", "0"}, AttemptDurations: make([]metrics.Distribution, 6)}
	a.AttemptDurations[0].Add(time.Millisecond)
	a.AttemptDurations[1].Add(100 * time.Millisecond)
	a.AttemptDurations[3].Add(200 * time.Millisecond)
	cp := engine.Checkpoint{RunID: "r", Sequence: 1, Cut: cut, Desired: 10}
	return history.Report{Record: history.Record{Summary: history.Summary{RunID: "r", DurableCut: &cut}, Checkpoint: &cp, Snapshot: domain.RunSnapshot{Draft: domain.Draft{Profile: domain.Profile{Mode: "open", RetryAccounting: "within"}}}}, Aggregates: []engine.Dimension{{Kind: "overall", Aggregate: a}, {Kind: "template", TemplateID: "t", Aggregate: a}}, Buckets: []engine.Bucket{{Index: 0, EndNS: 500000000, Desired: 5, Initial: 2, Retries: 1, Durations: a.AttemptDurations[1]}, {Index: 1, StartNS: 500000000, EndNS: 1000000000, Initial: 1}, {TemplateID: "t", Index: 0, EndNS: 500000000, Initial: 2}}}
}

func TestBuild(t *testing.T) {
	h := fixture()
	r, e := Build(h, Options{Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r.Summary.HTTPErrors != "2" || r.Summary.FailedAttempts != "2" || r.Summary.AttemptLatency.Count != "3" {
		t.Fatalf("bad populations %+v", r.Summary)
	}
	if len(r.Points) != 1 || *r.Points[0].TotalRate != 6 || *r.Points[0].Shortfall != 4 || r.Range.TotalBuckets != "2" || *r.Range.NextIndex != "1" {
		t.Fatalf("bad range %+v", r)
	}
	if r.Points[0].CPU != nil {
		t.Fatal("fabricated CPU")
	}
	full, _ := Build(h, Options{})
	if len(full.Points) != 2 {
		t.Fatal("export truncation")
	}
	scoped, _ := Build(h, Options{TemplateID: "t"})
	if len(scoped.Points) != 1 || scoped.Points[0].DesiredRate != nil {
		t.Fatal("scope mixed")
	}
	success, _ := Build(h, Options{Population: "success"})
	if success.Summary.AttemptLatency.Count != "1" {
		t.Fatal("population")
	}
	empty, _ := Build(history.Report{}, Options{})
	if empty.Summary.AttemptLatency.P50MS != nil {
		t.Fatal("invented latency")
	}
	t.Log("exact populations, partial intervals, scope and omissions verified")
}

func TestRecoveryOverlay(t *testing.T) {
	h := fixture()
	h.Recovery = &history.Recovery{Unknown: []engine.Unfinished{{Sequence: 8, TemplateID: "t", Active: true}}}
	r, _ := Build(h, Options{})
	if r.Summary.UnknownAttempts != "1" || r.Summary.LogicalUnknown != "1" {
		t.Fatal("recovery missing")
	}
	if h.Aggregates[0].Aggregate.Attempts[5] != "0" {
		t.Fatal("mutated stored evidence")
	}
	h.Recovery = nil
	cp := *h.Checkpoint
	cp.Cut = cp.Cut.Add(time.Second)
	cp.Sequence++
	cp.State = "completed"
	cp.Aggregates = []engine.Dimension{{Kind: "overall", Aggregate: engine.Aggregate{Initial: 9}}}
	out := Overlay(h, cp)
	r, _ = Build(out, Options{})
	if r.Durable || r.Summary.Initial != "9" || len(out.Aggregates) != 2 || len(out.Buckets) != 3 {
		t.Fatal("overlay lost evidence")
	}
	if Overlay(out, *h.Checkpoint).Checkpoint.Sequence != cp.Sequence {
		t.Fatal("stale overlay")
	}
	t.Log("durable cut retained; stale snapshots rejected")
}

func TestCompareExact(t *testing.T) {
	l, _ := Build(fixture(), Options{})
	r := l
	l.Summary.Initial = "9007199254740993"
	r.Summary.Initial = "9007199254740994"
	l.Summary.Retries = "0"
	r.Summary.Retries = "4"
	c, e := Compare(l, r, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range c.Deltas {
		if d.Key == "summary.initial" && d.Absolute != "1" {
			t.Fatal("precision loss")
		}
		if d.Key == "summary.retries" && d.Percent != nil {
			t.Fatal("zero baseline")
		}
	}
	if _, e = Compare(l, r, map[string]string{"missing": "t"}); e == nil {
		t.Fatal("invalid mapping")
	}
}

func embedded(t *testing.T, b []byte) any {
	t.Helper()
	s := string(b)
	start := strings.Index(s, "id=\"highloader-report-data\">") + len("id=\"highloader-report-data\">")
	end := strings.Index(s[start:], "</script>") + start
	var v any
	if e := json.Unmarshal([]byte(s[start:end]), &v); e != nil {
		t.Fatal(e)
	}
	return v
}

func TestExport(t *testing.T) {
	r, _ := Build(fixture(), Options{})
	secret := "SENTINEL-credential-925"
	r.Snapshot.Draft.Auth = domain.Auth{Mode: "bearer", Token: secret}
	r.Snapshot.Draft.URL = "https://example.invalid/?api_key=" + secret
	r.Snapshot.Draft.Templates = []domain.TemplateSource{{Filename: "</script><script>alert(1)</script>", Raw: []byte("invalid JSON " + secret)}}
	r.Range.TotalSamples = "1"
	r.Samples = []engine.Sample{{Response: metrics.Body{Data: []byte("echo " + secret)}}}
	js, css := []byte("/* TEST ONLY fixture */"), []byte("/* TEST ONLY fixture */")
	out, e := Export(r, false, js, css)
	if e != nil {
		t.Fatal(e)
	}
	v := embedded(t, out)
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), secret) || strings.Contains(string(out), "<script>alert(1)") {
		t.Fatal("secret or injection")
	}
	m := v.(map[string]any)
	samples := m["samples"].([]any)
	body := samples[0].(map[string]any)["Response"].(map[string]any)
	decoded, _ := base64.StdEncoding.DecodeString(body["Data"].(string))
	if strings.Contains(string(decoded), secret) {
		t.Fatal("encoded secret")
	}
	included, e := Export(r, true, js, css)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(included), secret) {
		t.Fatal("explicit inclusion changed data")
	}
	if r.Snapshot.Draft.Auth.Token != secret {
		t.Fatal("mutated original")
	}
	if _, e = Export(r, false, nil, css); e == nil {
		t.Fatal("missing asset accepted")
	}
	t.Log("inert hostile data, decoded body secrets and explicit inclusion verified")
}

func TestCPUScopesAndPartialExport(t *testing.T) {
	h := fixture()
	h.Buckets[0].CPU = ptr(20)
	h.Buckets[1].CPU = ptr(40)
	r, e := Build(h, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if r.Summary.CPU.Count != "2" || *r.Summary.CPU.MeanPercent != 30 || len(r.ChartScopes[""]) != 2 || len(r.ChartScopes["t"]) != 1 {
		t.Fatal("CPU or scope omission")
	}
	page, _ := Build(h, Options{Limit: 1})
	if _, e = Export(page, false, []byte("fixture"), []byte("fixture")); e == nil {
		t.Fatal("partial export accepted")
	}
	filtered, _ := Build(h, Options{Population: "http"})
	if filtered.Points[0].P50MS != nil {
		t.Fatal("unavailable filtered bucket latency invented")
	}
}

func TestRedactionCustomHeaderAndBasic(t *testing.T) {
	r, _ := Build(fixture(), Options{})
	secret := "CUSTOM-SECRET-824"
	r.Snapshot.Draft.Auth = domain.Auth{Mode: "api_key", Name: "X-Custom", Value: secret}
	r.Snapshot.Draft.Headers = []domain.HeaderRow{{Name: "X-Custom", Value: secret}}
	r.Snapshot.Effective = []domain.EffectiveRequest{{URL: "https://example.invalid/?X-Custom=" + secret}}
	r.Range.TotalSamples = "1"
	r.Samples = []engine.Sample{{RequestHeaders: map[string][]string{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("user:password-SENTINEL"))}}, Response: metrics.Body{Data: []byte(secret)}}}
	out, e := Export(r, false, []byte("/* test fixture */"), []byte("/* test fixture */"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(out), secret) || strings.Contains(string(out), base64.StdEncoding.EncodeToString([]byte("user:password-SENTINEL"))) {
		t.Fatal("secret retained")
	}
	c, _ := Compare(r, r, nil)
	if _, e = Export(c, false, []byte("/* test fixture */"), []byte("/* test fixture */")); e != nil {
		t.Fatal(e)
	}
}

func TestSourceFidelity(t *testing.T) {
	clean := []byte("// comment\n{\"number\":9007199254740993,\"body\":\"keep me\",}")
	withAuth := []byte("/* @highloader {\"headers\":{\"Authorization\":\"Bearer FILE-SECRET-284\",\"X-Keep\":\"useful\"}} */\n{\"number\":9007199254740993,\"body\":\"keep me\"}")
	for _, source := range [][]byte{clean, withAuth} {
		r, _ := Build(fixture(), Options{})
		r.Snapshot.Draft.Templates = []domain.TemplateSource{{Raw: source}}
		out, e := Export(r, false, []byte("test"), []byte("test"))
		if e != nil {
			t.Fatal(e)
		}
		tree := embedded(t, out).(map[string]any)
		sources := tree["snapshot"].(map[string]any)["draft"].(map[string]any)["templates"].([]any)
		encoded := sources[0].(map[string]any)["raw"].(string)
		decoded, _ := base64.StdEncoding.DecodeString(encoded)
		if !strings.Contains(string(decoded), "9007199254740993") || !strings.Contains(string(decoded), "keep me") {
			t.Fatalf("body lost: %s", decoded)
		}
		if string(source) == string(clean) && string(decoded) != string(clean) {
			t.Fatal("safe source changed")
		}
		if strings.Contains(string(decoded), "FILE-SECRET-284") {
			t.Fatal("metadata credential retained")
		}
		included, _ := Export(r, true, []byte("test"), []byte("test"))
		tree = embedded(t, included).(map[string]any)
		sources = tree["snapshot"].(map[string]any)["draft"].(map[string]any)["templates"].([]any)
		decoded, _ = base64.StdEncoding.DecodeString(sources[0].(map[string]any)["raw"].(string))
		if string(decoded) != string(source) {
			t.Fatal("inclusion source changed")
		}
	}
}

func TestComparisonMeasurements(t *testing.T) {
	h := fixture()
	h.Buckets[0].CPU = ptr(20)
	l, _ := Build(h, Options{})
	r, _ := Build(h, Options{})
	l.Snapshot.Draft.Templates = []domain.TemplateSource{{Body: json.RawMessage(`{"n":9007199254740992}`)}}
	r.Snapshot.Draft.Templates = []domain.TemplateSource{{Body: json.RawMessage(`{"n":9007199254740993}`)}}
	r.Summary.AttemptLatency.MeanMS = ptr(*l.Summary.AttemptLatency.MeanMS + 2)
	r.Summary.CPU.MeanPercent = ptr(30)
	c, e := Compare(l, r, nil)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, d := range c.Differences {
		if d.Path == "draft.templates" {
			found = true
			b, _ := json.Marshal(d)
			if !strings.Contains(string(b), "9007199254740993") {
				t.Fatal("difference rounded")
			}
		}
	}
	if !found {
		t.Fatal("large integer difference missed")
	}
	seen := map[string]bool{}
	for _, d := range c.Deltas {
		if d.Key == "summary.attemptLatency.meanMs" && d.Absolute == float64(2) {
			seen["latency"] = true
		}
		if d.Key == "summary.cpu.meanPercent" && d.Absolute == float64(10) {
			seen["cpu"] = true
		}
		if d.Key == "summary.observedIntervals.totalPerSecond" && d.Absolute == float64(0) {
			seen["rate"] = true
		}
	}
	if len(seen) != 3 {
		t.Fatalf("missing deltas %v", seen)
	}
}

func TestTruncatedSecretEcho(t *testing.T) {
	r, _ := Build(fixture(), Options{})
	r.Snapshot.Draft.Auth = domain.Auth{Mode: "api_key", Name: "X-Custom", Value: "FULL-CREDENTIAL-994"}
	r.Range.TotalSamples = "1"
	r.Samples = []engine.Sample{{ResponseHeaders: map[string][]string{"x-cUsToM": {"SECOND-CREDENTIAL-772"}}, Response: metrics.Body{Data: []byte("FULL-CRED"), Truncated: true}}}
	out, e := Export(r, false, []byte("test"), []byte("test"))
	if e != nil {
		t.Fatal(e)
	}
	tree := embedded(t, out).(map[string]any)
	sample := tree["samples"].([]any)[0].(map[string]any)
	body := sample["Response"].(map[string]any)
	decoded, _ := base64.StdEncoding.DecodeString(body["Data"].(string))
	if strings.Contains(string(decoded), "FULL-CRED") || strings.Contains(string(out), "SECOND-CREDENTIAL-772") {
		t.Fatal("partial/custom secret retained")
	}
}
