package metrics

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestDistributionBound(t *testing.T) {
	for i := 1001; i < len(edges); i++ {
		lower := edges[i-1]*1000 + 1
		upper := edges[i] * 1000
		if edges[i] > math.MaxInt64/1000 {
			upper = math.MaxInt64
		}
		if float64(upper)/float64(lower) > 1.01 {
			t.Fatalf("edge %d exceeds bound", i)
		}
		for _, v := range []int64{lower, upper} {
			var d Distribution
			if err := d.Add(time.Duration(v)); err != nil {
				t.Fatal(err)
			}
			q, ok := d.Quantile(.99)
			if !ok || q < time.Duration(v) || float64(q) > 1.01*float64(v) {
				t.Fatalf("observation=%d quantile=%d", v, q)
			}
		}
	}
	t.Logf("verified %d bounded edges across duration domain", len(edges))
}

func TestDistributionMerge(t *testing.T) {
	var all, left, right Distribution
	values := []time.Duration{0, 1, 999, 1000, 999999, 1000000, 1000001, 1004999, 1005000, 2000000, 999999999, time.Duration(math.MaxInt64)}
	for i, v := range values {
		_ = all.Add(v)
		if i%2 == 0 {
			_ = left.Add(v)
		} else {
			_ = right.Add(v)
		}
	}
	if err := left.Merge(&right); err != nil {
		t.Fatal(err)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	for _, q := range []float64{0, .1, .5, .9, .95, .99, 1} {
		got, _ := left.Quantile(q)
		rank := int(math.Ceil(q * float64(len(values))))
		if rank < 1 {
			rank = 1
		}
		exact := values[rank-1]
		if got < exact || (exact >= time.Millisecond && float64(got) > float64(exact)*1.01) {
			t.Fatalf("q=%v got=%v exact=%v", q, got, exact)
		}
		want, _ := all.Quantile(q)
		if got != want {
			t.Fatal("merge rank mismatch")
		}
	}
	encoded, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	var restored Distribution
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left.Summary(), restored.Summary()) {
		t.Fatal("moment roundtrip")
	}
	s := restored.Summary()
	if s.Count != 12 || *s.Min != 0 || *s.Max != time.Duration(math.MaxInt64) || s.ResolutionLimited != 3 {
		t.Fatalf("summary %+v", s)
	}
	var empty Distribution
	if _, ok := empty.Quantile(.5); ok || empty.Summary().MeanNS != nil {
		t.Fatal("invented empty data")
	}
	if empty.Add(-1) == nil {
		t.Fatal("negative accepted")
	}
	for _, q := range []float64{-1, 2, math.NaN()} {
		if _, ok := left.Quantile(q); ok {
			t.Fatal("invalid percentile accepted")
		}
	}
	before := string(encoded)
	if err = json.Unmarshal([]byte(`{"Version":2}`), &left); err == nil {
		t.Fatal("version accepted")
	}
	after, _ := json.Marshal(left)
	if string(after) != before {
		t.Fatal("failed decode mutated histogram")
	}
	t.Log("exact ranks, extrema and sparse roundtrip agree")
}

func TestAccounting(t *testing.T) {
	var a Aggregate
	_ = a.StartLogical()
	for i, status := range []int{503, 503, 200} {
		_ = a.Admit(i > 0)
		if err := a.FinishAttempt(Classify(status, false, false, false), time.Duration(i+1)*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	_ = a.FinishLogical(LogicalSuccess, 10*time.Millisecond)
	if a.Initial != 1 || a.Retries != 2 || a.Attempts[HTTPError] != 2 || a.Logical[LogicalSuccess] != 1 {
		t.Fatal("retry accounting")
	}
	for _, o := range []Outcome{HTTPTechnicalError, Canceled, Unknown, TechnicalError} {
		_ = a.Admit(false)
		_ = a.FinishAttempt(o, time.Millisecond)
	}
	d, _ := a.Durations(DefaultPopulation)
	if d.Summary().Count != 5 {
		t.Fatal("incomplete durations included")
	}
	d, _ = a.Durations(HTTPPopulation | TechnicalPopulation)
	if d.Summary().Count != 4 {
		t.Fatal("overlap double counted")
	}
	if a.FinishAttempt(Success, 0) == nil {
		t.Fatal("unadmitted completion")
	}
	if a.FinishLogical(LogicalFailure, 0) == nil {
		t.Fatal("duplicate logical terminal")
	}
	if Classify(200, true, false, false) != TechnicalError || Classify(503, true, false, false) != HTTPTechnicalError || Classify(0, false, false, false) != TechnicalError {
		t.Fatal("classification")
	}
	t.Log("1 logical success, 3 sends, 2 retries, 2 HTTP failures; overlap union and exclusions agree")
}

func TestBucketsAndStages(t *testing.T) {
	b, _ := NewBuckets(2, 2)
	var stage0, stage1 Aggregate
	_ = stage0.Admit(false)
	_ = stage0.StartLogical()
	b.Current().Initial = 1
	if err := b.Seal(time.Second); err != nil {
		t.Fatal(err)
	}
	_ = stage0.FinishAttempt(HTTPError, 1500*time.Millisecond)
	_ = stage1.Admit(true)
	_ = stage1.FinishAttempt(Success, 100*time.Millisecond)
	_ = stage0.FinishLogical(LogicalSuccess, 1600*time.Millisecond)
	b.Current().Retries = 1
	b.Current().Errors = 1
	_ = b.Current().Durations.Add(1500 * time.Millisecond)
	if err := b.Seal(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	points := b.Live()
	if *points[0].TotalRate != 1 || *points[1].TotalRate != 2 || points[0].P50 != nil || points[1].P50 == nil {
		t.Fatal("dispatch/completion buckets")
	}
	if stage0.Attempts[HTTPError] != 1 || stage1.Attempts[Success] != 1 || stage0.Logical[LogicalSuccess] != 1 {
		t.Fatal("stage attribution")
	}
	if err := b.Seal(1500 * time.Millisecond); err != ErrBackpressure {
		t.Fatal("pending limit")
	}
	snapshot := b.Checkpoint()
	_ = snapshot[1].Durations.Add(99 * time.Second)
	if b.Checkpoint()[1].Durations.Summary().Count != 1 {
		t.Fatal("aliased checkpoint")
	}
	if err := b.Ack(1); err != nil {
		t.Fatal(err)
	}
	if len(b.Checkpoint()) != 0 {
		t.Fatal("durable distribution retained")
	}
	if err := b.Seal(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	points = b.Live()
	if len(points) != 2 || points[1].TotalRate != nil {
		t.Fatal("zero duration rate")
	}
	if b.Ack(20) == nil {
		t.Fatal("future ack accepted")
	}
	t.Log("stage attribution and completion axis separate; partial rate=2; acknowledged histograms evicted")
}

func TestSamples(t *testing.T) {
	s, _ := NewSamples(1, 2, 3)
	total := uint64(10)
	candidate := Sample{TemplateID: "a", LogicalID: "actual", Outcome: Success, Request: Body{Data: []byte("abcdef"), Present: true, Total: &total}, Response: Body{Data: []byte{255, 0, 2, 3}, Present: true}, RequestHeaders: map[string][]string{"X-Test": {"original"}}}
	if !s.Retain(candidate) || s.Retain(candidate) {
		t.Fatal("success quota")
	}
	candidate.Request.Data[0] = 'z'
	candidate.RequestHeaders["X-Test"][0] = "changed"
	candidate.Outcome = Canceled
	if !s.Retain(candidate) {
		t.Fatal("canceled quota")
	}
	candidate.Outcome = HTTPError
	if !s.Retain(candidate) || s.Retain(candidate) {
		t.Fatal("failure quota")
	}
	candidate.Outcome = Unknown
	if s.Retain(candidate) {
		t.Fatal("unknown sample")
	}
	out := s.All()
	if len(out) != 3 || string(out[0].Request.Data) != "abc" || !out[0].Request.Truncated || out[0].Request.Captured != 3 || !out[0].Response.Binary || out[0].RequestHeaders["X-Test"][0] != "original" || out[0].LogicalID != "actual" {
		t.Fatal("retention metadata")
	}
	out[0].Request.Data[0] = 'x'
	if string(s.All()[0].Request.Data) != "abc" {
		t.Fatal("snapshot alias")
	}
	zero, _ := NewSamples(0, 0, 0)
	if zero.Retain(candidate) {
		t.Fatal("zero quota")
	}
	if CaptureBody(Body{}, 3).Present {
		t.Fatal("absent fabricated")
	}
	t.Log("exact 1/2 quotas, cancellation label, copied actual body and truncation metadata verified")
}

func TestValidation(t *testing.T) {
	var a Aggregate
	_ = a.Admit(false)
	if err := a.Observe(Observation{Status: 503, BodyReadFailure: true, Duration: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if a.Statuses[503] != 1 || a.BodyReadFailures != 1 || a.Attempts[HTTPTechnicalError] != 1 {
		t.Fatal("independent observations lost")
	}
	if a.Observe(Observation{Status: 900}) == nil {
		t.Fatal("invalid status")
	}
	if a.Observe(Observation{Status: 200}) == nil {
		t.Fatal("duplicate observation")
	}
	for _, o := range []Observation{{NetworkFailure: true}, {AttemptTimeout: true}, {DeadlineInterruption: true}, {Canceled: true}, {Unknown: true}} {
		_ = a.Admit(false)
		if err := a.Observe(o); err != nil {
			t.Fatal(err)
		}
	}
	if a.NetworkFailures != 1 || a.AttemptTimeouts != 1 || a.DeadlineInterruptions != 1 {
		t.Fatal("reason observations")
	}
	if a.FinishAttempt(Outcome(99), 0) == nil || a.FinishLogical(LogicalOutcome(99), 0) == nil {
		t.Fatal("invalid outcome")
	}
	if _, err := a.Durations(128); err == nil {
		t.Fatal("invalid population")
	}
	a.Initial = math.MaxUint64
	a.Retries = 0
	if a.Admit(false) != ErrOverflow {
		t.Fatal("admission overflow")
	}
	a.Started = math.MaxUint64
	if a.StartLogical() != ErrOverflow {
		t.Fatal("logical overflow")
	}
	var d Distribution
	_ = d.Add(time.Millisecond)
	var full = Distribution{count: math.MaxUint64}
	if full.Add(0) != ErrOverflow || full.Merge(&d) != ErrOverflow || d.Merge(nil) != ErrInvalid {
		t.Fatal("distribution overflow")
	}
	for _, bad := range []string{`{`, `{"Version":1,"count":"1","Bins":[]}`, `{"Version":1,"count":"1","Bins":[{"index":-1,"count":"1"}]}`, `{"Version":1,"count":"1","Bins":[{"index":0,"count":"1"}],"Min":1000,"Max":1000,"Sum":1000}`, `{"Version":1,"count":"0","Sum":1}`} {
		if json.Unmarshal([]byte(bad), &d) == nil {
			t.Fatalf("accepted corruption %s", bad)
		}
	}
	if _, err := NewBuckets(0, 1); err == nil {
		t.Fatal("invalid bucket capacity")
	}
	b, _ := NewBuckets(1, 1)
	if b.Seal(-1) == nil || b.Seal(2*time.Second) == nil {
		t.Fatal("invalid interval")
	}
	if b.Ack(-1) != nil {
		t.Fatal("idempotent ack")
	}
	if _, err := NewSamples(-1, 0, 0); err == nil {
		t.Fatal("invalid sample quota")
	}
	body := CaptureBody(Body{Data: []byte("x")}, -1)
	if body.Captured != 0 || !body.Truncated {
		t.Fatal("negative capture cap")
	}
	t.Log("overflow, corrupt persistence and inadmissible completions rejected")
}

func TestNonstandardStatus(t *testing.T) {
	var a Aggregate
	for _, status := range []int{600, 999} {
		_ = a.Admit(false)
		if err := a.Observe(Observation{Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	if a.Attempts[HTTPError] != 2 || a.Statuses[600] != 1 || a.Statuses[999] != 1 {
		t.Fatal("nonstandard status lost")
	}
	t.Log("600 and 999 preserved as HTTP error observations")
}

func TestTechnicalObserved(t *testing.T) {
	var a Aggregate
	_ = a.Admit(false)
	if err := a.Observe(Observation{Status: 503, NetworkFailure: true, BodyReadFailure: true, Canceled: true, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if a.TechnicalObserved != 1 || a.Attempts[Canceled] != 1 || a.Statuses[503] != 1 || a.AttemptDurations[Canceled].Summary().Count != 0 {
		t.Fatal(a)
	}
	t.Log("canceled multi-flag observation contributes one technical union, known503, zero default latency")
}
