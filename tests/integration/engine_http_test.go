package integration

import (
	"context"
	"encoding/json"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/request"
	"highloader/internal/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type recordingSink struct {
	mu   sync.Mutex
	last engine.Checkpoint
}

func (s *recordingSink) Commit(_ context.Context, c engine.Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last = c
	return nil
}

func TestEngineRecording(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	total := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[string(body)]++
		number := seen[string(body)]
		total++
		mu.Unlock()
		if number < 3 {
			w.WriteHeader(503)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	d := domain.NewDraft()
	d.URL = server.URL
	d.Capacity = 8
	d.Profile.Steps = []domain.Step{{DurationSeconds: 1, Level: 10}}
	d.Profile.RetryAccounting = "above"
	d.Policy.MaxRetries = 2
	d.Policy.DelayMS = 0
	d.Policy.TimeoutMS = 500
	d.Policy.DeadlineMS = 1500
	d.Templates = []domain.TemplateSource{{ID: "t", Valid: true, Body: json.RawMessage(`{"seq":"{{sys.request.seq}}","uuid":"{{sys.uuid}}"}`)}}
	d.SelectedIDs = []string{"t"}
	d.Settings["t"] = domain.TemplateSettings{Weight: 1}
	snapshot, err := request.Freeze(d, "integration", "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := transport.New(int(d.Capacity))
	defer client.Close()
	sink := &recordingSink{}
	runner, err := engine.New(snapshot, engine.Options{Transport: client, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := runner.View()
	mu.Lock()
	defer mu.Unlock()
	if !view.Durable || !view.WorkersReleased || view.State != "completed" || int(view.Aggregate.Initial+view.Aggregate.Retries) != total {
		t.Fatal(view, total)
	}
	for body, count := range seen {
		if count > 3 {
			t.Fatal("hidden attempt", body, count)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.last.Final || len(sink.last.Unfinished) != 0 || len(sink.last.Samples) == 0 {
		t.Fatal("missing final evidence")
	}
	for _, sample := range sink.last.Samples {
		if seen[string(sample.Request.Data)] == 0 {
			t.Fatal("sample does not match transmitted body")
		}
	}
	t.Logf("%d wire attempts = %d initial + %d retry; %d frozen logical bodies; durable terminal checkpoint", total, view.Aggregate.Initial, view.Aggregate.Retries, len(seen))
}
