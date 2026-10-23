package transport

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type readBarrier struct {
	io.ReadCloser
	entered chan struct{}
}

func (b *readBarrier) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	return b.ReadCloser.Read(p)
}

func attempt(url string) engine.Attempt {
	return engine.Attempt{RunID: "run", Sequence: 1, Number: 1, Started: time.Now(), Timeout: time.Second, Request: domain.EffectiveRequest{TemplateID: "t", URL: url, Method: "GET"}, Body: []byte(`{"value":1234567890123456789}`), BodyCap: 8}
}

func TestStream(t *testing.T) {
	var calls atomic.Int32
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		received, _ = io.ReadAll(r.Body)
		if r.Host != "custom.test" {
			t.Error("host override", r.Host)
		}
		if r.Header.Get("Accept-Encoding") != "" {
			t.Error("implicit compression")
		}
		w.Header().Set("Location", "/another")
		w.WriteHeader(302)
		_, _ = io.WriteString(w, strings.Repeat("x", 100000))
	}))
	defer server.Close()
	client := New(2)
	defer client.Close()
	a := attempt(server.URL)
	a.Request.Headers = []domain.EffectiveHeader{{Name: "Host", Value: "custom.test"}}
	r := client.DoAttempt(context.Background(), a)
	if calls.Load() != 1 || r.Observation.Status != 302 || len(received) != len(a.Body) || r.Sample.Response.Total == nil || *r.Sample.Response.Total != 100000 || len(r.Sample.Response.Data) != 8 || !r.Sample.Response.Truncated {
		t.Fatal(calls.Load(), r)
	}
	t.Log("GET body intact, Host applied, 302 not followed, 100000 bytes consumed with 8 retained")
}

func TestCancellation(t *testing.T) {
	headers := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.(http.Flusher).Flush()
		close(headers)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := New(1)
	defer client.Close()
	reading := make(chan struct{}, 1)
	client.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response, err := client.transport.RoundTrip(req)
		if response != nil {
			response.Body = &readBarrier{ReadCloser: response.Body, entered: reading}
		}
		return response, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan engine.Result, 1)
	go func() { done <- client.DoAttempt(ctx, attempt(server.URL)) }()
	<-headers
	<-reading
	cancel()
	select {
	case r := <-done:
		if r.Observation.Status != 503 || !r.Observation.Canceled {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("body cancellation hung")
	}
	t.Log("canceled body operation retains received 503 and releases promptly")
}

func TestNoReplay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int32
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		reader := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			req, e := http.ReadRequest(reader)
			if e != nil {
				conn.Close()
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			req.Body.Close()
			requests.Add(1)
			if i == 0 {
				_, _ = fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}")
			} else {
				conn.Close()
			}
		}
		listener.(*net.TCPListener).SetDeadline(time.Now().Add(150 * time.Millisecond))
		conn, e = listener.Accept()
		if e == nil {
			requests.Add(1)
			conn.Close()
		}
	}()
	client := New(1)
	defer client.Close()
	a := attempt("http://" + listener.Addr().String())
	if result := client.DoAttempt(context.Background(), a); result.Observation.Status != 200 {
		t.Fatal(result)
	}
	a.Number = 2
	a.Started = time.Now()
	result := client.DoAttempt(context.Background(), a)
	<-finished
	if requests.Load() != 2 || !result.Observation.NetworkFailure {
		t.Fatal(requests.Load(), result)
	}
	t.Log("pooled failure produced exactly two requests for two permits; zero hidden replay")
}

func TestTimeoutPhases(t *testing.T) {
	t.Run("headers", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
		defer server.Close()
		c := New(1)
		defer c.Close()
		a := attempt(server.URL)
		a.Timeout = 20 * time.Millisecond
		r := c.DoAttempt(context.Background(), a)
		if !r.Observation.AttemptTimeout || r.Observation.Status != 0 {
			t.Fatal(r)
		}
	})
	t.Run("tls", func(t *testing.T) {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer listener.Close()
		closed := make(chan struct{})
		go func() {
			conn, e := listener.Accept()
			if e == nil {
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn)
			}
			close(closed)
		}()
		c := New(1)
		defer c.Close()
		a := attempt("https://" + listener.Addr().String())
		a.Timeout = 20 * time.Millisecond
		r := c.DoAttempt(context.Background(), a)
		if !r.Observation.AttemptTimeout {
			t.Fatal(r)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("TLS connection retained")
		}
	})
	t.Run("dial", func(t *testing.T) {
		c := New(1)
		defer c.Close()
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		a := attempt("http://127.0.0.1:1")
		a.Timeout = 20 * time.Millisecond
		r := c.DoAttempt(context.Background(), a)
		if !r.Observation.AttemptTimeout {
			t.Fatal(r)
		}
	})
	t.Log("header, TLS and injected dial waits honor timeout")
}

func TestPartialBody(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		req, e := http.ReadRequest(bufio.NewReader(conn))
		if e != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_, _ = fmt.Fprint(conn, "HTTP/1.1 503 Unavailable\r\nContent-Length: 10\r\n\r\nabc")
	}()
	c := New(1)
	defer c.Close()
	r := c.DoAttempt(context.Background(), attempt("http://"+listener.Addr().String()))
	if r.Observation.Status != 503 || !r.Observation.BodyReadFailure || string(r.Sample.Response.Data) != "abc" {
		t.Fatal(r)
	}
	t.Log("received 503 and incomplete body both preserved")
}

func TestTLSVerificationAndReuse(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "{}")
	}))
	server.Config.ConnState = func(net.Conn, http.ConnState) {}
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	untrusted := New(1)
	result := untrusted.DoAttempt(context.Background(), attempt(server.URL))
	untrusted.Close()
	if !result.Observation.NetworkFailure || result.Observation.Status != 0 {
		t.Fatal("accepted untrusted certificate", result)
	}
	trusted := New(1)
	defer trusted.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	trusted.transport.TLSClientConfig.RootCAs = roots
	for i := 0; i < 2; i++ {
		a := attempt(server.URL)
		a.Number = uint64(i + 1)
		if r := trusted.DoAttempt(context.Background(), a); r.Observation.Status != 200 || r.Observation.NetworkFailure {
			t.Fatal(r)
		}
	}
	if connections.Load() != 2 {
		t.Fatal("TLS pool was canceled after successful attempt", connections.Load())
	}
	t.Log("untrusted root rejected; trusted requests reuse one socket across canceled completed contexts")
}

func TestDialReleaseBarrier(t *testing.T) {
	c := New(1)
	defer c.Close()
	canceled := make(chan struct{})
	release := make(chan struct{})
	c.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}
	a := attempt("http://127.0.0.1:1")
	a.Timeout = 20 * time.Millisecond
	done := make(chan engine.Result, 1)
	go func() { done <- c.DoAttempt(context.Background(), a) }()
	<-canceled
	select {
	case <-done:
		t.Fatal("attempt returned with dial helper still active")
	default:
	}
	close(release)
	select {
	case r := <-done:
		if !r.Observation.AttemptTimeout {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("join failed")
	}
	scope := &dialScope{done: make(chan struct{})}
	scope.closeAndWait()
	if scope.enter() {
		t.Fatal("late helper admitted")
	}
	t.Log("timeout result withheld until dial cleanup; late callback admission rejected")
}

func TestAbsoluteCutoff(t *testing.T) {
	c := New(1)
	defer c.Close()
	var dials atomic.Int32
	var observed time.Time
	c.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		observed, _ = ctx.Deadline()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	a := attempt("http://127.0.0.1:1")
	a.Started = time.Now().Add(-80 * time.Millisecond)
	a.Timeout = 100 * time.Millisecond
	result := c.DoAttempt(context.Background(), a)
	if !observed.Equal(a.Started.Add(a.Timeout)) || !result.Observation.AttemptTimeout {
		t.Fatal(observed, result)
	}
	a.Started = time.Now().Add(-time.Second)
	a.LogicalDeadline = true
	result = c.DoAttempt(context.Background(), a)
	if dials.Load() != 1 || !result.Observation.DeadlineInterruption || result.Ended.IsZero() {
		t.Fatal("expired permit performed network I/O", dials.Load(), result)
	}
	if result.Sample.TemplateID != a.Request.TemplateID || result.Sample.LogicalID == "" || result.Sample.Sequence != a.Sequence || result.Sample.Attempt != a.Number || !result.Sample.Request.Present || result.Sample.Response.Present || result.Sample.Status != nil || !result.Sample.Ended.Equal(result.Ended) {
		t.Fatal("expired permit lost diagnostic identity", result.Sample)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = c.DoAttempt(ctx, a)
	if !result.Observation.Canceled || result.Observation.DeadlineInterruption || result.Observation.AttemptTimeout || result.Sample.Outcome != metrics.Canceled || result.Sample.LogicalID == "" || dials.Load() != 1 {
		t.Fatal("pre-entry cancellation classification/identity", result)
	}
	t.Log("delayed entry uses original absolute cutoff; preexpired permit never dials")
}
