package transport

import (
	"context"
	"errors"
	"highloader/internal/domain"
	"highloader/internal/engine"
	"highloader/internal/metrics"
	"net"
	"testing"
	"time"
)

func TestInvalidRequestNoIO(t *testing.T) {
	// Invalid configuration -> no dial -> classified terminal evidence.
	c := New(0)
	defer c.Close()
	calls := 0
	c.dial = func(context.Context, string, string) (net.Conn, error) {
		calls++
		return nil, errors.New("unexpected dial")
	}
	for _, req := range []domain.EffectiveRequest{{Method: "BAD METHOD", URL: "http://fixture.invalid"}, {Method: "GET", URL: ":bad"}, {Method: "GET", URL: "ftp://fixture.invalid"}} {
		a := engine.Attempt{Sequence: 4, Number: 2, Started: time.Now(), Timeout: time.Second, Request: req, Body: []byte("body"), BodyCap: 2}
		r := c.DoAttempt(context.Background(), a)
		if calls != 0 || !r.Observation.NetworkFailure || r.Sample.Outcome != metrics.TechnicalError || r.Sequence != 4 || r.Number != 2 || string(r.Sample.Request.Data) != "bo" || !r.Sample.Request.Truncated {
			t.Fatalf("unexpected result %+v dials=%d", r, calls)
		}
	}
	t.Log("Invalid requests produce bounded technical evidence with zero dial calls")
}
