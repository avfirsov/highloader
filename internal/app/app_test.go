package app

import (
	"context"
	"encoding/json"
	"errors"
	"highloader/internal/api"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/transport"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, url string, edit func(*Options)) (*Application, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "body.json"), []byte(`{"n":9007199254740993}`), 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "history.sqlite")
	store, e := history.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	o := Options{Store: store, Logger: log.New(testWriter{t}, "", 0), Version: "test", PickFolder: func() (string, bool, error) { return dir, false, nil }}
	if edit != nil {
		edit(&o)
	}
	a, e := New(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ctx, c := context.WithTimeout(context.Background(), time.Second); defer c(); _ = a.Close(ctx) })
	imported, e := a.Import(context.Background(), 0)
	if e != nil {
		t.Fatal(e)
	}
	d := imported.Draft
	d.URL = url
	d.Profile = domain.Profile{Mode: "closed", Steps: []domain.Step{{DurationSeconds: 60, Level: 1}}, RetryAccounting: "within"}
	d.Capacity = 1
	if _, e = a.Update(d.Revision, editor(d)); e != nil {
		t.Fatal(e)
	}
	return a, path
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) { w.t.Log(string(b)); return len(b), nil }
func editor(d domain.Draft) api.EditableDraft {
	b, _ := json.Marshal(d)
	var e api.EditableDraft
	_ = json.Unmarshal(b, &e)
	return e
}
func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for boundary")
	}
}

func TestDurableStartAndRestart(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var sends atomic.Int64
	var store *history.Store
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		rows, e := store.List(context.Background(), 10, 0)
		if e != nil || len(rows) != 1 {
			t.Error("traffic before durable start", e, len(rows))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"n":9007199254740993}` {
			t.Error("source lost precision", string(body))
		}
		once.Do(func() { close(entered) })
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	a, path := fixture(t, server.URL, nil)
	store = a.options.Store
	rev := a.Draft().Revision
	browser, cancel := context.WithCancel(context.Background())
	first, e := a.Start(browser, rev, "lost-response")
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	await(t, entered)
	second, e := a.Start(context.Background(), 0, "lost-response")
	if e != nil || *second.RunID != *first.RunID {
		t.Fatal("retry changed run", e)
	}
	if _, e = a.Start(context.Background(), rev, "other"); e == nil {
		t.Fatal("overlapping run accepted")
	}
	if _, e = a.Update(rev, editor(a.Draft())); e == nil {
		t.Fatal("active mutation accepted")
	}
	if e = a.Clear(context.Background()); e == nil {
		t.Fatal("active clear accepted")
	}
	v, e := a.Stop(context.Background(), *first.RunID, false)
	if e != nil || v.State != "draining" {
		t.Fatal(v.State, e)
	}
	close(release)
	await(t, a.active.released)
	if sends.Load() != 1 {
		t.Fatalf("expected one send, got %d", sends.Load())
	}
	if !a.active.runner.View().Durable {
		t.Fatal("terminal not durable")
	}
	if e = a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	reopened, e := history.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.Recover(context.Background(), time.Now()); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{Store: reopened, PickFolder: func() (string, bool, error) { return "", true, nil }})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close(context.Background())
	retry, e := b.Start(context.Background(), 999, "lost-response")
	if e != nil || *retry.RunID != *first.RunID {
		t.Fatal("restart retry failed", e)
	}
	if len(b.Draft().Templates) != 0 || sends.Load() != 1 {
		t.Fatal("retry unexpectedly rebuilt or dispatched")
	}
	repeated, e := b.Repeat(context.Background(), *first.RunID, 0)
	if e != nil || string(repeated.Templates[0].Raw) != `{"n":9007199254740993}` {
		t.Fatal("repeat lost original", e)
	}
	t.Log("First traffic followed commit; retry and restart preserved one identity and one send")
}

func TestReservationAndDetachedDraft(t *testing.T) {
	a, _ := fixture(t, "http://127.0.0.1:1", nil)
	d := a.Draft()
	d.Templates[0].Raw[0] = 'x'
	if a.Draft().Templates[0].Raw[0] != '{' {
		t.Fatal("draft aliases originals")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	a.options.PickFolder = func() (string, bool, error) { close(entered); <-release; return "", true, nil }
	done := make(chan error, 1)
	go func() { _, e := a.Import(context.Background(), a.Draft().Revision); done <- e }()
	await(t, entered)
	if _, e := a.Update(d.Revision, editor(d)); e == nil {
		t.Fatal("mutation raced import")
	}
	if e := a.Clear(context.Background()); e == nil {
		t.Fatal("clear raced reservation")
	}
	_ = a.Draft()
	if _, e := a.History(context.Background(), 10, 0); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if _, e := a.Update(d.Revision-1, editor(d)); e == nil {
		t.Fatal("stale revision accepted")
	}
	t.Log("Slow dialog reserves mutation while reads and detached sources remain available")
}

type blockingSink struct {
	store            *history.Store
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockingSink) Commit(ctx context.Context, cp engine.Checkpoint) error {
	if cp.Final {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.store.Commit(ctx, cp)
}

func TestFinalizationReadAndShutdown(t *testing.T) {
	var sink *blockingSink
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) {
		sink = &blockingSink{store: o.Store, entered: make(chan struct{}), release: make(chan struct{})}
		o.Sink = sink
	})
	active, e := a.Start(context.Background(), a.Draft().Revision, "finalize")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Stop(context.Background(), *active.RunID, false); e != nil {
		t.Fatal(e)
	}
	await(t, sink.entered)
	read := make(chan struct{})
	go func() { _ = a.Active(); _ = a.Draft(); _, _ = a.History(context.Background(), 10, 0); close(read) }()
	await(t, read)
	if _, e = a.Start(context.Background(), a.Draft().Revision, "blocked"); e == nil {
		t.Fatal("start during finalization")
	}
	if e = a.Delete(context.Background(), *active.RunID); e == nil {
		t.Fatal("delete during finalization")
	}
	closed := make(chan struct{})
	a.options.CloseCPU = func() error {
		if _, e := a.options.Store.List(context.Background(), 10, 0); e != nil {
			t.Error("history closed before CPU cleanup", e)
		}
		close(closed)
		return nil
	}
	close(sink.release)
	await(t, a.active.released)
	if e = a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	await(t, closed)
	t.Log("Reads bypass blocked sink; new work blocked until durable finalization and resources join")
}

func TestFailedPersistenceBlocksNewRun(t *testing.T) {
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) { o.Sink = failingSink{} })
	active, e := a.Start(context.Background(), a.Draft().Revision, "failed")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = a.Stop(context.Background(), *active.RunID, true)
	await(t, a.active.released)
	if _, e = a.Start(context.Background(), a.Draft().Revision, "next"); e == nil {
		t.Fatal("unsaved terminal permitted next run")
	}
	if e = a.Close(context.Background()); e == nil {
		t.Fatal("shutdown hid unsaved finalization")
	}
	t.Log("Unsaved terminal blocks new work and surfaces shutdown error")
}

type failingSink struct{}

func (failingSink) Commit(context.Context, engine.Checkpoint) error {
	return errors.New("injected failure")
}

func TestConcurrentStartReservation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) {
		o.NewTransport = func(n int) (engine.Transport, func()) {
			close(entered)
			<-release
			c := transport.New(n)
			return c, c.Close
		}
	})
	browser, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := a.Start(browser, a.Draft().Revision, "reserved-start"); done <- e }()
	await(t, entered)
	if _, e := a.Start(context.Background(), a.Draft().Revision, "raced-start"); e == nil {
		t.Fatal("two starts reserved")
	}
	if e := a.Clear(context.Background()); e == nil {
		t.Fatal("clear raced not-yet-persisted start")
	}
	_ = a.Draft()
	if _, e := a.History(context.Background(), 10, 0); e != nil {
		t.Fatal(e)
	}
	cancel()
	close(release)
	if e := <-done; e != nil {
		t.Fatal("browser response loss canceled owned start", e)
	}
	current := a.Active()
	_, _ = a.Stop(context.Background(), *current.RunID, true)
	await(t, a.active.released)
	rows, e := a.options.Store.List(context.Background(), 10, 0)
	if e != nil || len(rows) != 1 {
		t.Fatal("expected one durable start", len(rows), e)
	}
	t.Log("One reservation excludes raced start/clear and survives browser cancellation")
}

func TestExitKeepsForceControl(t *testing.T) {
	entered, releaseServer := make(chan struct{}), make(chan struct{})
	// Client cancellation does not assert that target-side work stopped.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-releaseServer }))
	defer func() { close(releaseServer); server.Close() }()
	exited := make(chan struct{})
	a, _ := fixture(t, server.URL, func(o *Options) { o.RequestExit = func() { close(exited) } })
	active, e := a.Start(context.Background(), a.Draft().Revision, "exit-run")
	if e != nil {
		t.Fatal(e)
	}
	await(t, entered)
	h := api.Handler(a, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/exit", strings.NewReader(`{}`)))
	if w.Code != 202 || !a.Active().Closing {
		t.Fatal("exit not acknowledged with closed admission", w.Code)
	}
	a.Exit() // repeated exit shares one graceful drain and one callback
	v, e := a.Stop(context.Background(), *active.RunID, false)
	if e != nil || v.State != "draining" {
		t.Fatal(v.State, e)
	}
	select {
	case <-exited:
		t.Fatal("exit closed controls before work released")
	default:
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/runs/"+*active.RunID+"/force-stop", strings.NewReader(`{}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	await(t, exited)
	await(t, a.active.released)
	if !a.Active().View.Durable {
		t.Fatal("exit signaled before durable completion")
	}
	t.Log("Exit preserves HTTP force control until durable worker/transport release")
}

func TestExitBeforeReservedCommit(t *testing.T) {
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var sends atomic.Int64
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) {
		o.RequestExit = func() { close(exited) }
		o.NewTransport = func(n int) (engine.Transport, func()) {
			close(entered)
			<-release
			c := transport.New(n)
			return countingTransport{inner: c, count: &sends}, c.Close
		}
	})
	result := make(chan api.Active, 1)
	failure := make(chan error, 1)
	go func() {
		v, e := a.Start(context.Background(), a.Draft().Revision, "exit-reserved")
		result <- v
		failure <- e
	}()
	await(t, entered)
	a.Exit()
	if !a.Active().Closing {
		t.Fatal("exit did not close admission")
	}
	close(release)
	active := <-result
	if e := <-failure; e != nil {
		t.Fatal(e)
	}
	await(t, exited)
	await(t, a.active.released)
	if sends.Load() != 0 {
		t.Fatal("reserved start dispatched after exit acknowledgement", sends.Load())
	}
	record, e := a.options.Store.Get(context.Background(), *active.RunID)
	if e != nil || record.Checkpoint == nil || !record.Checkpoint.Final || record.State != "stopped" {
		t.Fatal("durable start was orphaned", record.State, e)
	}
	t.Log("Exit before reserved commit produces durable stopped evidence and zero traffic")
}

type countingTransport struct {
	inner engine.Transport
	count *atomic.Int64
}

func (c countingTransport) DoAttempt(ctx context.Context, a engine.Attempt) engine.Result {
	c.count.Add(1)
	return c.inner.DoAttempt(ctx, a)
}

func TestDiscardRequiresActualRelease(t *testing.T) {
	exited := make(chan struct{}, 1)
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) { o.Sink = failingSink{}; o.RequestExit = func() { exited <- struct{}{} } })
	if e := a.DiscardAndExit(); e == nil {
		t.Fatal("discard without closing")
	}
	active, e := a.Start(context.Background(), a.Draft().Revision, "discard")
	if e != nil {
		t.Fatal(e)
	}
	a.Exit()
	a.mu.Lock()
	r := a.active
	a.mu.Unlock()
	await(t, r.released)
	v := r.runner.View()
	if v.Durable || !v.WorkersReleased || !v.WriterReleased {
		t.Fatalf("bad failure release %+v", v)
	}
	select {
	case <-exited:
		t.Fatal("automatic discard")
	default:
	}
	if e = a.DiscardAndExit(); e != nil {
		t.Fatal(e)
	}
	await(t, func() <-chan struct{} {
		done := make(chan struct{})
		go func() { <-exited; close(done) }()
		return done
	}())
	if e = a.DiscardAndExit(); e != nil {
		t.Fatal(e)
	}
	saved, e := a.options.Store.Get(context.Background(), *active.RunID)
	if e != nil || saved.RunID != *active.RunID {
		t.Fatal("durable record removed")
	}
	if e = a.Close(context.Background()); e != nil {
		t.Fatalf("acknowledged warning returned: %v", e)
	}
	t.Log("explicit unsaved exit after actual release preserves durable record")
}

func TestDiscardCannotBypassWriter(t *testing.T) {
	var sink *blockingSink
	a, _ := fixture(t, "http://127.0.0.1:1", func(o *Options) {
		sink = &blockingSink{store: o.Store, entered: make(chan struct{}), release: make(chan struct{})}
		o.Sink = sink
	})
	active, e := a.Start(context.Background(), a.Draft().Revision, "discard-guard")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Stop(context.Background(), *active.RunID, true); e != nil {
		t.Fatal(e)
	}
	await(t, sink.entered)
	a.Exit()
	e = a.DiscardAndExit()
	close(sink.release)
	await(t, a.active.released)
	if e == nil {
		t.Fatal("discard bypassed live writer")
	}
	if e = a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestHistoricalViewReleased(t *testing.T) {
	a, _ := fixture(t, "http://127.0.0.1:1", nil)
	snapshot := domain.RunSnapshot{SchemaVersion: 1, RunID: "historical", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Draft: a.Draft()}
	if _, e := a.options.Store.Start(context.Background(), snapshot, "historic"); e != nil {
		t.Fatal(e)
	}
	if e := a.options.Store.Commit(context.Background(), engine.Checkpoint{RunID: "historical", Sequence: 1, Cut: time.Now().UTC(), State: "completed", Final: true}); e != nil {
		t.Fatal(e)
	}
	v, e := a.View(context.Background(), "historical")
	if e != nil || !v.WorkersReleased || !v.WriterReleased || !v.Durable {
		t.Fatalf("historical ownership %+v %v", v, e)
	}
}
