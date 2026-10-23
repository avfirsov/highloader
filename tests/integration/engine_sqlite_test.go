package integration

import (
	"context"
	"encoding/json"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/metrics"
	"highloader/internal/request"
	"highloader/internal/transport"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type engineTransportFunc func(context.Context, engine.Attempt) engine.Result

func (f engineTransportFunc) DoAttempt(ctx context.Context, a engine.Attempt) engine.Result {
	return f(ctx, a)
}

type joinedStore struct {
	store  *history.Store
	finals atomic.Int32
}

func (s *joinedStore) Commit(ctx context.Context, cp engine.Checkpoint) error {
	if cp.Final {
		s.finals.Add(1)
	}
	return s.store.Commit(ctx, cp)
}

func TestEngineSQLiteLateForce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := domain.NewDraft()
	d.URL = "http://127.0.0.1:1/"
	d.Capacity = 1
	d.Profile.Mode = "closed"
	d.Profile.Steps = []domain.Step{{DurationSeconds: 30, Level: 1}}
	d.Policy.TimeoutMS = 3000
	d.Policy.DeadlineMS = 5000
	d.Templates = []domain.TemplateSource{{ID: "t", Valid: true, Body: json.RawMessage(`{"v":1}`)}}
	d.SelectedIDs = []string{"t"}
	d.Settings["t"] = domain.TemplateSettings{Weight: 1}
	snapshot, err := request.Freeze(d, "late-force", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(ctx, snapshot, "key"); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	sink := &joinedStore{store: store}
	runner, err := engine.New(snapshot, engine.Options{Sink: sink, ReleaseTimeout: 20 * time.Millisecond, Transport: engineTransportFunc(func(_ context.Context, a engine.Attempt) engine.Result {
		close(entered)
		<-release
		ended := time.Now()
		status := 503
		return engine.Result{Ended: ended, Observation: metrics.Observation{Status: status, NetworkFailure: true, BodyReadFailure: true}, Sample: metrics.Sample{RunID: a.RunID, TemplateID: a.Request.TemplateID, LogicalID: "1", Sequence: a.Sequence, Attempt: a.Number, Started: a.Started, Ended: ended, Duration: ended.Sub(a.Started), Status: &status}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(context.Background()) }()
	<-entered
	if _, err = runner.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	var record history.Record
	for {
		record, err = store.Get(ctx, snapshot.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if record.Checkpoint != nil && record.Checkpoint.State == "application_failed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("nonfinal failure checkpoint missing")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if sink.finals.Load() != 0 || record.Checkpoint.Final || len(record.Checkpoint.Unfinished) != 1 || !record.Checkpoint.Unfinished[0].OutcomeCommitted {
		t.Fatal("early final or missing releasing marker", record.Checkpoint)
	}
	other := snapshot
	other.RunID = "blocked-replacement"
	if _, err = store.Start(ctx, other, "other"); !errors.Is(err, history.ErrActive) {
		t.Fatal("reservation released early", err)
	}
	select {
	case <-runner.Done():
		t.Fatal("worker falsely released")
	default:
	}
	// Reproduce the exact crash checkpoint in a separate store, with no live coordinator.
	recovered, err := history.Open(filepath.Join(t.TempDir(), "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.Start(ctx, snapshot, "recovery"); err != nil {
		t.Fatal(err)
	}
	if err = recovered.Commit(ctx, *record.Checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err = recovered.Recover(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	crash, err := recovered.Get(ctx, snapshot.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if crash.Recovery == nil || len(crash.Recovery.Unknown) != 0 {
		t.Fatal("canceled plus unknown double count", crash.Recovery)
	}
	recovered.Close()
	close(release)
	select {
	case err = <-finished:
		if err == nil {
			t.Fatal("missing worker release fault")
		}
	case <-ctx.Done():
		t.Fatal("engine did not join")
	}
	view := runner.View()
	if !view.Durable || !view.WorkersReleased || !view.WriterReleased || view.PersistenceError != "" || sink.finals.Load() != 1 {
		t.Fatal("terminal evidence not durable exactly once", view, sink.finals.Load())
	}
	report, err := store.ReadReport(ctx, snapshot.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checkpoint == nil || !report.Checkpoint.Final || len(report.Checkpoint.Unfinished) != 0 {
		t.Fatal("invalid SQL terminal")
	}
	var overall engine.Aggregate
	for _, a := range report.Aggregates {
		if a.Kind == "overall" {
			overall = a.Aggregate
		}
	}
	if overall.Attempts[metrics.Canceled] != "1" || overall.Attempts[metrics.Unknown] != "0" || overall.Statuses["503"] != "1" || overall.TechnicalObserved != 1 || overall.AttemptDurations[metrics.Canceled].Summary().Count != 0 {
		t.Fatal(overall)
	}
	var canceled uint64
	for _, delta := range report.Completions {
		if delta.TemplateID == "" {
			canceled += uint64(delta.Value.Attempts[metrics.Canceled])
			distribution, _ := delta.Value.AttemptDistribution(metrics.DefaultPopulation)
			if distribution.Summary().Count != 0 {
				t.Fatal("canceled latency included")
			}
		}
	}
	if canceled != 1 || len(report.Samples) != 1 || report.Samples[0].Status == nil || *report.Samples[0].Status != 503 {
		t.Fatal("late evidence missing", canceled, report.Samples)
	}
	if _, err = store.Start(ctx, other, "other"); err != nil {
		t.Fatal("reservation not released after durable final", err)
	}
	t.Log("real SQL held reservation through watchdog; one durable final; canceled1/unknown0/known503/technicalUnion1; crash tombstone adds no unknown")
}

type evidenceClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *evidenceClock) Now() time.Time                         { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *evidenceClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (c *evidenceClock) set(at time.Time)                       { c.mu.Lock(); c.now = at; c.mu.Unlock() }

func TestEngineSQLiteDelayedEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	store, err := history.Open(filepath.Join(t.TempDir(), "late.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := domain.NewDraft()
	d.URL = "http://127.0.0.1:1/"
	d.Capacity = 1
	d.Profile.Steps = []domain.Step{{DurationSeconds: 3, Level: 1000}}
	d.Policy.TimeoutMS = 2000
	d.Policy.DeadlineMS = 2500
	d.Samples.Success = 0
	d.Samples.Failure = 0
	d.Templates = []domain.TemplateSource{{ID: "t", Valid: true, Body: json.RawMessage(`{}`)}}
	d.SelectedIDs = []string{"t"}
	d.Settings["t"] = domain.TemplateSettings{Weight: 1}
	snapshot, err := request.Freeze(d, "late-event", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(ctx, snapshot, "late"); err != nil {
		t.Fatal(err)
	}
	origin := time.Unix(1000, 0)
	clock := &evidenceClock{now: origin}
	entered := make(chan struct{})
	release := make(chan struct{})
	runner, err := engine.New(snapshot, engine.Options{Clock: clock, Sink: store, Transport: engineTransportFunc(func(context.Context, engine.Attempt) engine.Result {
		close(entered)
		<-release
		return engine.Result{Ended: origin.Add(999 * time.Millisecond), Observation: metrics.Observation{Status: 200}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runner.Run(context.Background()) }()
	<-entered
	clock.set(origin.Add(1001 * time.Millisecond))
	for {
		record, err := store.Get(ctx, snapshot.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if record.LatestSequence > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("base0 not durable")
		case <-time.After(time.Millisecond):
		}
	}
	close(release)
	if _, err = runner.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("join")
	}
	report, err := store.ReadReport(ctx, snapshot.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, delta := range report.Completions {
		if delta.TemplateID != "" {
			continue
		}
		if delta.Index != 0 || delta.Value.Attempts[metrics.Success] != 1 || delta.Value.Logical[metrics.LogicalSuccess] != 1 {
			t.Fatal("wrong durable event interval", delta)
		}
		summary := delta.Value.LogicalDurations[metrics.LogicalSuccess].Summary()
		if summary.MeanNS == nil || *summary.MeanNS != float64(999*time.Millisecond) {
			t.Fatal(summary)
		}
		found = true
	}
	if !found {
		t.Fatal("missing durable completion")
	}
	for _, point := range runner.View().Points {
		if point.Index == 0 && point.Completions != 1 {
			t.Fatal("live0 missing delayed completion", point)
		}
		if point.Index == 1 && point.Completions != 0 {
			t.Fatal("processing interval got completion", point)
		}
	}
	t.Log("durable base0 then delayed999ms event merges into SQL/live0 once; bucket1 untouched; logical999ms")
}

func TestEngineSQLiteExpiredSamples(t *testing.T) {
	ctx := context.Background()
	store, err := history.Open(filepath.Join(t.TempDir(), "expired.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	d := domain.NewDraft()
	d.URL = "http://127.0.0.1:1/"
	d.Templates = []domain.TemplateSource{{ID: "t", Valid: true, Body: json.RawMessage(`{"v":1}`)}}
	d.SelectedIDs = []string{"t"}
	d.Settings["t"] = domain.TemplateSettings{Weight: 1}
	snapshot, err := request.Freeze(d, "expired", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(ctx, snapshot, "expired"); err != nil {
		t.Fatal(err)
	}
	client := transport.New(1)
	defer client.Close()
	cp := engine.Checkpoint{RunID: snapshot.RunID, Sequence: 1, Cut: time.Now(), State: "running"}
	for seq := uint64(1); seq <= 2; seq++ {
		a := engine.Attempt{RunID: snapshot.RunID, Sequence: seq, Number: 1, Started: time.Now().Add(-time.Second), Timeout: time.Millisecond, Request: snapshot.Effective[0], Body: []byte(`{"v":1}`), BodyCap: 4}
		result := client.DoAttempt(ctx, a)
		if !result.Observation.AttemptTimeout || result.Observation.NetworkFailure {
			t.Fatal(result)
		}
		cp.Samples = append(cp.Samples, engine.Sample(result.Sample))
	}
	if err = store.Commit(ctx, cp); err != nil {
		t.Fatal(err)
	}
	report, err := store.ReadReport(ctx, snapshot.RunID)
	if err != nil || len(report.Samples) != 2 {
		t.Fatal(err, report.Samples)
	}
	for _, s := range report.Samples {
		if s.TemplateID != "t" || s.Sequence == 0 || s.Attempt != 1 || s.LogicalID == "" || !s.Request.Present || len(s.Request.Data) != 4 || s.Response.Present || s.Status != nil {
			t.Fatal(s)
		}
	}
	t.Log("preexpired native permits retain two distinct SQL sample identities and bounded request prefixes; response absent")
}
