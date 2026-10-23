//go:build windows

package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/history"
	"highloader/internal/platform"
	"highloader/internal/request"
	"highloader/internal/transport"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type localFixture struct {
	server     *httptest.Server
	bodies     [][]byte
	count, bad atomic.Uint64
	delay      time.Duration
}
type cpuLatest struct {
	value atomic.Pointer[engine.CPUValue]
}

func (c *cpuLatest) Latest() engine.CPUValue {
	v := c.value.Load()
	if v == nil {
		return engine.CPUValue{Reason: "baseline pending"}
	}
	return *v
}

type observedSink struct {
	maxCPUAmendments                                                 int
	store                                                            *history.Store
	mu                                                               sync.Mutex
	maxUnfinished, maxBatches, maxDeltas, maxSamples, maxSampleBytes int
	maxWaiting                                                       int
}

func (s *observedSink) Commit(ctx context.Context, cp engine.Checkpoint) error {
	s.mu.Lock()
	s.maxCPUAmendments = max(s.maxCPUAmendments, len(cp.CPUAmendments))
	s.maxUnfinished = max(s.maxUnfinished, len(cp.Unfinished))
	waiting := 0
	for _, item := range cp.Unfinished {
		if !item.Active {
			waiting++
		}
	}
	s.maxWaiting = max(s.maxWaiting, waiting)
	s.maxBatches = max(s.maxBatches, len(cp.CompletionBatches))
	deltas := len(cp.UnflushedCompletion)
	for _, b := range cp.CompletionBatches {
		deltas += len(b.Deltas)
	}
	s.maxDeltas = max(s.maxDeltas, deltas)
	s.maxSamples = max(s.maxSamples, len(cp.Samples))
	size := 0
	for _, sample := range cp.Samples {
		size += len(sample.Request.Data) + len(sample.Response.Data)
	}
	s.maxSampleBytes = max(s.maxSampleBytes, size)
	s.mu.Unlock()
	return s.store.Commit(ctx, cp)
}

func newFixture(t *testing.T, delay time.Duration) *localFixture {
	f := &localFixture{delay: delay}
	for i := 0; i < 10; i++ {
		f.bodies = append(f.bodies, []byte(fmt.Sprintf(`{"id":%d,"payload":"%s"}`, i, strings.Repeat("x", 10219))))
	}
	response := bytes.Repeat([]byte("r"), 1024)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 10241))
		valid := false
		for _, expected := range f.bodies {
			if bytes.Equal(expected, body) {
				valid = true
				break
			}
		}
		if err != nil || !valid || r.Method != "POST" {
			f.bad.Add(1)
			http.Error(w, "invalid fixture request", 400)
			return
		}
		f.count.Add(1)
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if delay > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, _ = w.Write(response)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func performanceDraft(f *localFixture, mode string) domain.Draft {
	d := domain.NewDraft()
	d.URL = f.server.URL
	for i, body := range f.bodies {
		id := fmt.Sprint(i)
		d.Templates = append(d.Templates, domain.TemplateSource{ID: id, Filename: id + ".json", Raw: body, Body: json.RawMessage(body), Valid: true})
		d.SelectedIDs = append(d.SelectedIDs, id)
		d.Settings[id] = domain.TemplateSettings{Weight: 1}
	}
	switch mode {
	case "pacing_probe":
		d.Profile.Steps = []domain.Step{{DurationSeconds: 10, Level: 2000}}
	case "reference":
		d.Profile.Steps = []domain.Step{{DurationSeconds: 10, Level: 500}, {DurationSeconds: 60, Level: 2000}}
	case "closed":
		d.Profile.Mode = "closed"
		d.Profile.Steps = []domain.Step{{DurationSeconds: 10, Level: 1000}}
	case "saturation":
		d.Capacity = 8
		d.Policy.MaxRetries = 2
		d.Policy.DelayMS = 5000
		d.Policy.MaxDelayMS = 5000
		d.Profile.Steps = []domain.Step{{DurationSeconds: 60, Level: 2000}}
	case "memory":
		d.Profile.Steps = []domain.Step{{DurationSeconds: 300, Level: 500}}
	}
	return d
}

func sourceHash(root string) (string, error) {
	paths := []string{filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum")}
	for _, ownedRoot := range []string{"cmd", "internal", "web", "tests", "tools", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, ownedRoot), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".cache", "node_modules", "logs", "docs":
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if ext == ".go" || ext == ".ts" || ext == ".tsx" || ext == ".css" || ext == ".html" || ext == ".json" || ext == ".js" || ext == ".svg" || ext == ".png" || ext == ".woff" || ext == ".woff2" || ext == ".mjs" || ext == ".ps1" || filepath.Base(path) == "go.mod" || filepath.Base(path) == "go.sum" {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(raw))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func TestDiagnosticSourceHash(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd", "internal", "web/report-dist", "tests", "tools", "scripts", ".aws"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"go.mod", "go.sum", "web/vite.report.config.ts", "web/report-dist/report.js", ".aws/synthetic.json"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("initial"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before, err := sourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".aws/synthetic.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	same, err := sourceHash(root)
	if err != nil || same != before {
		t.Fatal("unowned input affected digest", err)
	}
	if err = os.WriteFile(filepath.Join(root, "web/report-dist/report.js"), []byte("new real asset"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := sourceHash(root)
	if err != nil || after == before {
		t.Fatal("embedded asset omitted", err)
	}
	t.Log("explicit owned roots include report assets and exclude unrelated protected directory")
}

func workingSet() (uint64, error) {
	type counters struct {
		Size, Faults                                                                    uint32
		Peak, Working, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile uintptr
	}
	c := counters{}
	c.Size = uint32(unsafe.Sizeof(c))
	kernel := syscall.NewLazyDLL("kernel32.dll")
	handle, _, _ := kernel.NewProc("GetCurrentProcess").Call()
	ok, _, err := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo").Call(handle, uintptr(unsafe.Pointer(&c)), uintptr(c.Size))
	if ok == 0 {
		return 0, err
	}
	return uint64(c.Working), nil
}

func hostMemory() (uint64, error) {
	type status struct {
		Length, Load                                                                                         uint32
		TotalPhysical, AvailablePhysical, TotalPage, AvailablePage, TotalVirtual, AvailableVirtual, Extended uint64
	}
	s := status{}
	s.Length = uint32(unsafe.Sizeof(s))
	ok, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&s)))
	if ok == 0 {
		return 0, err
	}
	return s.TotalPhysical, nil
}

type observation struct {
	Seconds                         float64 `json:"seconds"`
	Heap, RSS                       uint64
	Unfinished, ActiveUsers, Points int
	CPU                             *float64
}
type pacingProbeResult struct {
	Stage                                                     int
	Attempts                                                  uint64
	Seconds, RPS, TargetRPS, TargetFraction, RequiredFraction float64
	Meets95Percent                                            bool
}

type diagnostic struct {
	PacingProbe                                                      *pacingProbeResult `json:"pacingProbe,omitempty"`
	MaxCPUAmendments                                                 int
	SourceHashKind                                                   string
	HeapEarlyMean, HeapTailMean, RSSEarlyMean, RSSTailMean           float64
	HeapEarlyMin, HeapTailMin, RSSEarlyMin, RSSTailMin               uint64
	HostRAMBytes                                                     uint64
	ProcessorIdentifier, HostMetadataLimitation                      string
	Label, Mode, Go, OS, Arch, SourceHash                            string
	LogicalCPUs                                                      int
	Started, Ended                                                   time.Time
	ElapsedSeconds, StopAckMS                                        float64
	ServerRequests, InvalidRequests                                  uint64
	View                                                             engine.View
	Stages                                                           []engine.Dimension
	Observations                                                     []observation
	MaxHeap, MaxRSS                                                  uint64
	MaxUnfinished, MaxBatches, MaxDeltas, MaxSamples, MaxSampleBytes int
	MaxWaiting                                                       int
	SteadyAttempts                                                   uint64
	SteadySeconds, SteadyRPS, SteadyTargetFraction                   float64
}

func runDiagnostic(t *testing.T, mode string) {
	delay := time.Duration(0)
	if mode == "saturation" {
		delay = 2 * time.Second
	}
	fixture := newFixture(t, delay)
	draft := performanceDraft(fixture, mode)
	snapshot, err := request.Freeze(draft, "diagnostic-"+mode, "headless-native-diagnostic", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Start(context.Background(), snapshot, "diagnostic-start"); err != nil {
		t.Fatal(err)
	}
	sink := &observedSink{store: store}
	cpu, err := platform.OpenCPU()
	if err != nil {
		t.Fatal(err)
	}
	defer cpu.Close()
	latest := &cpuLatest{}
	client := transport.New(int(draft.Capacity))
	defer client.Close()
	runner, err := engine.New(snapshot, engine.Options{Transport: client, Sink: sink, CPU: latest})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := sourceHash(root)
	if err != nil {
		t.Fatal(err)
	}
	result := diagnostic{Label: "HEADLESS BACKEND DIAGNOSTIC; NOT UI-OPEN PACKAGED RELEASE ACCEPTANCE", Mode: mode, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, LogicalCPUs: runtime.NumCPU(), SourceHash: hash, Started: time.Now()}
	if mode == "pacing_probe" {
		result.Label += "; SHORT 10-SECOND PACING PROBE, NOT FULL REFERENCE"
	}
	result.SourceHashKind = "SHA256 of explicit owned compiler/config/assets inputs plus test harness; not packaged EXE hash"
	result.HostRAMBytes, err = hostMemory()
	if err != nil {
		t.Fatal(err)
	}
	result.ProcessorIdentifier = os.Getenv("PROCESSOR_IDENTIFIER")
	result.HostMetadataLimitation = "CPU marketing model and Windows edition/build unavailable: CIM access denied; no retry or bypass attempted"
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	go func() { done <- runner.Run(ctx) }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	stopped := false
	running := true
	for running {
		select {
		case err = <-done:
			running = false
		case <-ctx.Done():
			t.Fatal("diagnostic timeout")
		case <-ticker.C:
			sample := cpu.Sample()
			latest.value.Store(&engine.CPUValue{ID: sample.At.Format(time.RFC3339Nano), At: sample.At, Percent: sample.Percent, Reason: sample.MissingReason})
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			rss, e := workingSet()
			if e != nil {
				t.Fatal(e)
			}
			view := runner.View()
			point := observation{Seconds: time.Since(result.Started).Seconds(), Heap: memory.HeapAlloc, RSS: rss, Unfinished: view.Unfinished, ActiveUsers: view.ActiveUsers, Points: len(view.Points), CPU: sample.Percent}
			result.Observations = append(result.Observations, point)
			result.MaxHeap = max(result.MaxHeap, memory.HeapAlloc)
			result.MaxRSS = max(result.MaxRSS, rss)
			if view.Unfinished > int(draft.Capacity) || len(view.Points) > 600 {
				t.Fatal("bounded state violated", point)
			}
			if mode == "saturation" && !stopped && point.Seconds >= 10 {
				start := time.Now()
				stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, e = runner.Stop(stopCtx, false)
				if e == nil {
					_, e = runner.Stop(stopCtx, true)
				}
				stopCancel()
				result.StopAckMS = float64(time.Since(start)) / float64(time.Millisecond)
				if e != nil {
					t.Fatal(e)
				}
				stopped = true
			}
		}
	}
	result.Ended = time.Now()
	result.ElapsedSeconds = result.Ended.Sub(result.Started).Seconds()
	result.View = runner.View()
	result.ServerRequests = fixture.count.Load()
	result.InvalidRequests = fixture.bad.Load()
	sink.mu.Lock()
	result.MaxUnfinished = sink.maxUnfinished
	result.MaxBatches = sink.maxBatches
	result.MaxDeltas = sink.maxDeltas
	result.MaxSamples = sink.maxSamples
	result.MaxSampleBytes = sink.maxSampleBytes
	result.MaxWaiting = sink.maxWaiting
	result.MaxCPUAmendments = sink.maxCPUAmendments
	sink.mu.Unlock()
	if mode == "memory" {
		summarizeMemory(&result)
	}
	report, readErr := store.ReadReport(context.Background(), snapshot.RunID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, dim := range report.Aggregates {
		if dim.Kind == "stage" {
			result.Stages = append(result.Stages, dim)
			if mode == "pacing_probe" && dim.Stage == 0 {
				attempts := dim.Aggregate.Initial + dim.Aggregate.Retries
				rps := float64(attempts) / 10
				fraction := rps / 2000
				result.PacingProbe = &pacingProbeResult{Stage: 0, Attempts: attempts, Seconds: 10, RPS: rps, TargetRPS: 2000, TargetFraction: fraction, RequiredFraction: 0.95, Meets95Percent: fraction >= 0.95}
			}
			if mode == "reference" && dim.Stage == 1 {
				result.SteadyAttempts = dim.Aggregate.Initial + dim.Aggregate.Retries
				result.SteadySeconds = 60
				result.SteadyRPS = float64(result.SteadyAttempts) / 60
				result.SteadyTargetFraction = result.SteadyRPS / 2000
			}
		}
	}
	raw, jsonErr := json.MarshalIndent(result, "", "  ")
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if e := os.WriteFile(filepath.Join(root, "logs", "release-validation-"+mode+".json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("mode=%s elapsed=%.3fs sent=%d server=%d steadyRPS=%.3f targetFraction=%.5f schedulerMisses=%d capacityMisses=%d maxHeap=%d maxRSS=%d stopAckMS=%.3f sourceHash=%s", mode, result.ElapsedSeconds, result.View.Aggregate.Initial+result.View.Aggregate.Retries, result.ServerRequests, result.SteadyRPS, result.SteadyTargetFraction, result.View.SchedulerMisses, result.View.CapacityMisses, result.MaxHeap, result.MaxRSS, result.StopAckMS, hash)
	if p := result.PacingProbe; p != nil {
		t.Logf("stage=%d attempts=%d denominatorSeconds=%.0f measuredRPS=%.3f targetRPS=%.0f targetFraction=%.5f requiredFraction=%.2f meets95Percent=%t shortDiagnosticNotFullReference=true", p.Stage, p.Attempts, p.Seconds, p.RPS, p.TargetRPS, p.TargetFraction, p.RequiredFraction, p.Meets95Percent)
	}
	if mode == "pacing_probe" && result.PacingProbe == nil {
		t.Fatal("short pacing probe stage evidence missing; diagnostic preserved")
	}
	if err != nil {
		t.Fatal("engine failed; exact diagnostic preserved", err)
	}
	if !result.View.Durable || !result.View.WorkersReleased || !result.View.WriterReleased || result.InvalidRequests != 0 {
		t.Fatal("invalid finality/fixture", result.View, result.InvalidRequests)
	}
	if mode != "saturation" && result.ServerRequests != result.View.Aggregate.Initial+result.View.Aggregate.Retries {
		t.Fatal("server/dispatch mismatch")
	}
	if result.MaxSamples > 250 || result.MaxUnfinished > int(draft.Capacity) || result.MaxSampleBytes > 10*25*2*65536 || result.MaxBatches > 11 || result.MaxCPUAmendments > 12 {
		t.Fatal("retention bound violated")
	}
	if mode == "saturation" && (result.View.CapacityMisses == 0 || result.StopAckMS > 3000 || result.MaxWaiting == 0) {
		t.Fatal("saturation/stop not demonstrated")
	}
}

func summarizeMemory(result *diagnostic) {
	var early, tail int
	for _, p := range result.Observations {
		if p.Seconds >= 60 && p.Seconds < 120 {
			early++
			result.HeapEarlyMean += float64(p.Heap)
			result.RSSEarlyMean += float64(p.RSS)
			if result.HeapEarlyMin == 0 || p.Heap < result.HeapEarlyMin {
				result.HeapEarlyMin = p.Heap
			}
			if result.RSSEarlyMin == 0 || p.RSS < result.RSSEarlyMin {
				result.RSSEarlyMin = p.RSS
			}
		}
		if p.Seconds >= 240 {
			tail++
			result.HeapTailMean += float64(p.Heap)
			result.RSSTailMean += float64(p.RSS)
			if result.HeapTailMin == 0 || p.Heap < result.HeapTailMin {
				result.HeapTailMin = p.Heap
			}
			if result.RSSTailMin == 0 || p.RSS < result.RSSTailMin {
				result.RSSTailMin = p.RSS
			}
		}
	}
	if early > 0 {
		result.HeapEarlyMean /= float64(early)
		result.RSSEarlyMean /= float64(early)
	}
	if tail > 0 {
		result.HeapTailMean /= float64(tail)
		result.RSSTailMean /= float64(tail)
	}
}

func TestBackendDiagnostic(t *testing.T) {
	mode := os.Getenv("HIGHLOADER_DIAGNOSTIC")
	switch mode {
	case "pacing_probe", "reference", "closed", "saturation", "memory":
		runDiagnostic(t, mode)
	default:
		t.Skip("set HIGHLOADER_DIAGNOSTIC=pacing_probe|reference|closed|saturation|memory for explicit local headless diagnostics")
	}
}

func TestFixtureHeadroom(t *testing.T) {
	if os.Getenv("HIGHLOADER_DIAGNOSTIC") != "headroom" {
		t.Skip("explicit headroom mode")
	}
	fixture := newFixture(t, 0)
	tr := &http.Transport{MaxIdleConns: 64, MaxIdleConnsPerHost: 64, MaxConnsPerHost: 64}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	var completed, failures atomic.Uint64
	start := time.Now()
	deadline := start.Add(10 * time.Second)
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				response, err := client.Post(fixture.server.URL, "application/json", bytes.NewReader(fixture.bodies[index%10]))
				index++
				if err != nil {
					failures.Add(1)
					continue
				}
				n, err := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil || n != 1024 || response.StatusCode != 200 {
					failures.Add(1)
				} else {
					completed.Add(1)
				}
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	rps := float64(completed.Load()) / elapsed
	t.Logf("workers=32 elapsed=%.6f completed=%d server=%d errors=%d invalidBodies=%d rps=%.3f target2000Multiple=%.3f", elapsed, completed.Load(), fixture.count.Load(), failures.Load(), fixture.bad.Load(), rps, rps/2000)
	if failures.Load() != 0 || fixture.bad.Load() != 0 || rps <= 2000 {
		t.Fatal("fixture headroom not demonstrated")
	}
}
