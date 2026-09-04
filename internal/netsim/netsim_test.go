package netsim

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// spyRoundTripper records whether it was ever called, so tests can prove
// a dropped request never reaches the underlying transport at all.
type spyRoundTripper struct {
	calls int
	resp  *http.Response
	err   error
}

func (s *spyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	return req
}

func TestConditions_ZeroValueIsDisabled(t *testing.T) {
	tr := NewTransport(&spyRoundTripper{}, Conditions{}, rand.New(rand.NewSource(1)))
	if tr.Enabled() {
		t.Fatal("expected the zero-value Conditions to report disabled")
	}
}

func TestTransport_LatencyIsAdded(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{Latency: 40 * time.Millisecond}, rand.New(rand.NewSource(1)))

	start := time.Now()
	if _, err := tr.RoundTrip(newRequest(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("expected at least 40ms of injected latency, got %v", elapsed)
	}
	if spy.calls != 1 {
		t.Fatalf("expected the base transport to be called once, got %d", spy.calls)
	}
}

func TestTransport_JitterStaysWithinRange(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{Latency: 50 * time.Millisecond, Jitter: 20 * time.Millisecond}, rand.New(rand.NewSource(42)))

	for i := 0; i < 50; i++ {
		delay, drop := tr.sample()
		if drop {
			t.Fatalf("iteration %d: expected no drops with LossRate 0", i)
		}
		if delay < 30*time.Millisecond || delay > 70*time.Millisecond {
			t.Fatalf("iteration %d: delay %v outside expected [30ms,70ms] range", i, delay)
		}
	}
}

func TestTransport_JitterNeverGoesNegative(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	// Jitter larger than Latency would go negative without clamping.
	tr := NewTransport(spy, Conditions{Latency: 5 * time.Millisecond, Jitter: 50 * time.Millisecond}, rand.New(rand.NewSource(7)))

	for i := 0; i < 100; i++ {
		delay, _ := tr.sample()
		if delay < 0 {
			t.Fatalf("iteration %d: got negative delay %v", i, delay)
		}
	}
}

func TestTransport_LossRateOneAlwaysDropsWithoutCallingBase(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{LossRate: 1.0}, rand.New(rand.NewSource(1)))

	_, err := tr.RoundTrip(newRequest(t))
	if err == nil {
		t.Fatal("expected a simulated drop to return an error")
	}
	if spy.calls != 0 {
		t.Fatalf("expected the base transport to never be called for a dropped request, got %d calls", spy.calls)
	}
}

func TestTransport_LossRateZeroNeverDrops(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{LossRate: 0}, rand.New(rand.NewSource(1)))

	for i := 0; i < 100; i++ {
		if _, err := tr.RoundTrip(newRequest(t)); err != nil {
			t.Fatalf("iteration %d: unexpected drop with LossRate 0: %v", i, err)
		}
	}
	if spy.calls != 100 {
		t.Fatalf("expected all 100 requests to reach the base transport, got %d", spy.calls)
	}
}

func TestTransport_StatsTrackRequestsAndDrops(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{LossRate: 1.0}, rand.New(rand.NewSource(1)))

	for i := 0; i < 5; i++ {
		tr.RoundTrip(newRequest(t))
	}

	stats := tr.Snapshot()
	if stats.Requests != 5 || stats.Dropped != 5 {
		t.Fatalf("expected Requests=5 Dropped=5, got %+v", stats)
	}
}

func TestTransport_ContextCancellationDuringDelayReturnsPromptly(t *testing.T) {
	spy := &spyRoundTripper{resp: &http.Response{StatusCode: 200}}
	tr := NewTransport(spy, Conditions{Latency: 5 * time.Second}, rand.New(rand.NewSource(1)))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := newRequest(t).WithContext(ctx)

	start := time.Now()
	_, err := tr.RoundTrip(req)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected RoundTrip to return promptly on context cancellation, took %v", elapsed)
	}
	if spy.calls != 0 {
		t.Fatalf("expected the base transport to never be reached when the context expires during the delay, got %d calls", spy.calls)
	}
}

// TestTransport_EndToEndOverRealServer confirms the wrapper behaves
// correctly plugged into a real http.Client against a real httptest
// server, not just against the spy.
func TestTransport_EndToEndOverRealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := NewTransport(http.DefaultTransport, Conditions{Latency: 20 * time.Millisecond}, rand.New(rand.NewSource(1)))
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}

	start := time.Now()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("expected the real request to also pay the injected latency, got %v", elapsed)
	}
}
