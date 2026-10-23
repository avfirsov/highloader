package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	client    *http.Client
	transport *http.Transport
	dial      func(context.Context, string, string) (net.Conn, error)
}
type attemptContextKey struct{}

func New(capacity int) *Client {
	if capacity < 1 {
		capacity = 1
	}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: capacity, MaxIdleConnsPerHost: capacity, MaxConnsPerHost: capacity, IdleConnTimeout: 90 * time.Second, MaxResponseHeaderBytes: 1 << 20, DisableCompression: true, ForceAttemptHTTP2: false, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}}
	dialer := &net.Dialer{KeepAlive: 30 * time.Second}
	c := &Client{transport: tr, dial: dialer.DialContext}
	tr.DialContext = func(detached context.Context, network, address string) (net.Conn, error) {
		ctx, release := dialContext(detached)
		defer release()
		return c.dial(ctx, network, address)
	}
	tr.DialTLSContext = func(detached context.Context, network, address string) (net.Conn, error) {
		ctx, release := dialContext(detached)
		defer release()
		raw, err := c.dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		config := tr.TLSClientConfig.Clone()
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			raw.Close()
			return nil, err
		}
		if config.ServerName == "" {
			config.ServerName = host
		}
		conn := tls.Client(raw, config)
		if err = conn.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		return conn, nil
	}
	c.client = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c
}

func (c *Client) DoAttempt(parent context.Context, a engine.Attempt) engine.Result {
	ctx, cancel := context.WithDeadline(parent, a.Started.Add(a.Timeout))
	scope := &dialScope{done: make(chan struct{})}
	defer func() { cancel(); scope.closeAndWait() }()
	ctx = context.WithValue(ctx, attemptContextKey{}, ctx)
	ctx = context.WithValue(ctx, dialScopeKey{}, scope)
	r := engine.Result{Sequence: a.Sequence, Number: a.Number}
	s := metrics.Sample{RunID: a.RunID, TemplateID: a.Request.TemplateID, LogicalID: strconv.FormatUint(a.Sequence, 10), Sequence: a.Sequence, Attempt: a.Number, Started: a.Started, Method: a.Request.Method, URL: a.Request.URL}
	if a.UserID != nil {
		s.UserID = strconv.FormatUint(*a.UserID, 10)
	}
	total := uint64(len(a.Body))
	s.Request = metrics.CaptureBody(metrics.Body{Data: a.Body, Present: true, Total: &total}, a.BodyCap)
	if ctx.Err() != nil {
		r.Ended = time.Now()
		r.Observation.Duration = r.Ended.Sub(a.Started)
		if errors.Is(ctx.Err(), context.Canceled) {
			r.Observation.Canceled = true
		} else if a.LogicalDeadline {
			r.Observation.DeadlineInterruption = true
		} else {
			r.Observation.AttemptTimeout = true
		}
		s.Ended, s.Duration = r.Ended, r.Observation.Duration
		s.Outcome = metrics.Classify(0, !r.Observation.Canceled, r.Observation.Canceled, false)
		r.Sample = s
		return r
	}
	// An opaque ReadCloser prevents NewRequest from installing GetBody.
	req, err := http.NewRequestWithContext(ctx, a.Request.Method, a.Request.URL, io.NopCloser(bytes.NewReader(a.Body)))
	if err == nil {
		req.ContentLength = int64(len(a.Body))
		req.GetBody = nil
		for _, h := range a.Request.Headers {
			if !h.Deleted {
				if strings.EqualFold(h.Name, "Host") {
					req.Host = h.Value
				} else {
					req.Header.Set(h.Name, h.Value)
				}
			}
		}
		// Suppress Go's implicit user agent unless explicitly configured.
		if _, ok := req.Header["User-Agent"]; !ok {
			req.Header["User-Agent"] = []string{""}
		}
		s.RequestHeaders = req.Header.Clone()
		s.RequestHeaders["Content-Length"] = []string{strconv.Itoa(len(a.Body))}
		host := req.Host
		if host == "" {
			host = req.URL.Host
		}
		s.RequestHeaders["Host"] = []string{host}
		s.Request.ContentType = req.Header.Get("Content-Type")
		var resp *http.Response
		resp, err = c.client.Do(req)
		if resp != nil {
			status := resp.StatusCode
			s.Status = &status
			r.Observation.Status = status
			s.ResponseHeaders = resp.Header.Clone()
			s.Response = metrics.Body{Present: true, ContentType: resp.Header.Get("Content-Type")}
			buf := make([]byte, 32<<10)
			var received uint64
			for {
				n, e := resp.Body.Read(buf)
				if n > 0 {
					received += uint64(n)
					room := a.BodyCap - len(s.Response.Data)
					if room > n {
						room = n
					}
					if room > 0 {
						s.Response.Data = append(s.Response.Data, buf[:room]...)
					}
				}
				if e != nil {
					if e != io.EOF {
						err = e
						r.Observation.BodyReadFailure = true
					}
					break
				}
			}
			resp.Body.Close()
			s.Response.Total = &received
			s.Response = metrics.CaptureBody(s.Response, a.BodyCap)
		}
	}
	if err != nil {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			r.Observation.Canceled = true
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			if a.LogicalDeadline {
				r.Observation.DeadlineInterruption = true
			} else {
				r.Observation.AttemptTimeout = true
			}
		default:
			r.Observation.NetworkFailure = true
		}
	}
	r.Ended = time.Now()
	r.Observation.Duration = r.Ended.Sub(a.Started)
	if r.Observation.Duration < 0 {
		r.Observation.Duration = 0
	}
	s.Ended = r.Ended
	s.Duration = r.Observation.Duration
	s.Outcome = metrics.Classify(r.Observation.Status, r.Observation.NetworkFailure || r.Observation.AttemptTimeout || r.Observation.DeadlineInterruption || r.Observation.BodyReadFailure, r.Observation.Canceled, false)
	r.Sample = s
	return r
}

func (c *Client) Close() { c.transport.CloseIdleConnections() }

func dialContext(detached context.Context) (context.Context, func()) {
	// Go 1.24 transport.go uses WithoutCancel for pooled dials; values survive but request cancellation does not.
	original, ok := detached.Value(attemptContextKey{}).(context.Context)
	if !ok {
		original = detached
	}
	ctx, cancel := context.WithCancel(original)
	scope, _ := detached.Value(dialScopeKey{}).(*dialScope)
	if scope != nil && !scope.enter() {
		cancel()
		return ctx, func() {}
	}
	stop := context.AfterFunc(detached, cancel)
	return ctx, func() {
		stop()
		cancel()
		if scope != nil {
			scope.leave()
		}
	}
}
