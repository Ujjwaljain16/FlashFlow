package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestTrackedTransport_ResponseHeaderTimeout_BoundsHungBackend
// regression-tests F-14: RoundTrip had no ResponseHeaderTimeout, so a
// target that accepts a connection but never writes a response would hang
// the request indefinitely when RoundTrip is called directly (as
// proxy.ReverseProxy does) rather than through an http.Client.Timeout.
func TestTrackedTransport_ResponseHeaderTimeout_BoundsHungBackend(t *testing.T) {
	neverResponds := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-neverResponds // hang forever unless the test releases it
	}))
	defer ts.Close()
	defer close(neverResponds)

	cfg := DefaultTransportConfig("test_pool_hung")
	cfg.ResponseHeaderTimeout = 100 * time.Millisecond
	tt := NewTrackedTransport(cfg)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		resp, err := tt.RoundTrip(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected RoundTrip to fail once ResponseHeaderTimeout elapsed, got nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("RoundTrip did not return within 2s of a 100ms ResponseHeaderTimeout -- appears unbounded")
	}
}

func TestTrackedTransport_ConnectionReuse_KeepAliveEnabled(t *testing.T) {
	// Start a test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg := DefaultTransportConfig("test_pool")
	cfg.DisableKeepAlives = false
	tt := NewTrackedTransport(cfg)
	client := tt.HTTPClient(5 * time.Second)

	// Issue 10 serial requests
	for i := 0; i < 10; i++ {
		resp, err := client.Get(ts.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}

	stats := tt.Snapshot()
	if stats.RequestsCompleted != 10 {
		t.Fatalf("expected 10 completed requests, got %d", stats.RequestsCompleted)
	}
	// For serial requests over keep-alive connection, exactly 1 connection should be dialed.
	if stats.SuccessfulDials != 1 {
		t.Fatalf("expected exactly 1 dial for 10 serial keep-alive requests, got %d", stats.SuccessfulDials)
	}
	if stats.RequestsPerConn != 10.0 {
		t.Fatalf("expected 10.0 requests per connection, got %f", stats.RequestsPerConn)
	}
}

func TestTrackedTransport_ConnectionReuse_KeepAliveDisabled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg := DefaultTransportConfig("test_no_keepalive")
	cfg.DisableKeepAlives = true
	tt := NewTrackedTransport(cfg)
	client := tt.HTTPClient(5 * time.Second)

	// Issue 5 serial requests
	for i := 0; i < 5; i++ {
		resp, err := client.Get(ts.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}

	stats := tt.Snapshot()
	if stats.RequestsCompleted != 5 {
		t.Fatalf("expected 5 completed requests, got %d", stats.RequestsCompleted)
	}
	// With keep-alives disabled, each request requires a new TCP dial.
	if stats.SuccessfulDials != 5 {
		t.Fatalf("expected 5 dials for 5 non-keepalive requests, got %d", stats.SuccessfulDials)
	}
	if stats.RequestsPerConn != 1.0 {
		t.Fatalf("expected 1.0 request per connection, got %f", stats.RequestsPerConn)
	}
}

func TestTrackedTransport_ConcurrentConnections(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg := DefaultTransportConfig("test_concurrent")
	tt := NewTrackedTransport(cfg)
	client := tt.HTTPClient(5 * time.Second)

	concurrency := 10
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			resp, err := client.Get(ts.URL)
			if err != nil {
				t.Errorf("concurrent request failed: %v", err)
				return
			}
			_, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()

	stats := tt.Snapshot()
	if stats.RequestsCompleted != uint64(concurrency) {
		t.Fatalf("expected %d completed requests, got %d", concurrency, stats.RequestsCompleted)
	}
	// Concurrency = 10 simultaneous requests requires up to 10 connections established.
	if stats.SuccessfulDials == 0 || stats.SuccessfulDials > uint64(concurrency) {
		t.Fatalf("unexpected dial count: %d", stats.SuccessfulDials)
	}
}

func TestTrackedTransport_CloseIdleConnections_Lifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg := DefaultTransportConfig("test_lifecycle")
	cfg.DisableKeepAlives = false
	tt := NewTrackedTransport(cfg)
	client := tt.HTTPClient(5 * time.Second)

	// Make 3 requests over keep-alive
	for i := 0; i < 3; i++ {
		resp, err := client.Get(ts.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}

	initialStats := tt.Snapshot()
	if initialStats.SuccessfulDials != 1 {
		t.Fatalf("expected 1 dial for 3 keep-alive requests, got %d", initialStats.SuccessfulDials)
	}
	if initialStats.ActiveConns != 1 {
		t.Fatalf("expected 1 active connection in idle pool, got %d", initialStats.ActiveConns)
	}
	if initialStats.ClosedConns != 0 {
		t.Fatalf("expected 0 closed connections before pool close, got %d", initialStats.ClosedConns)
	}

	// Close all idle connections in pool
	tt.CloseIdleConnections()

	// Wait briefly for trackedConn.Close() callbacks to execute
	time.Sleep(20 * time.Millisecond)

	finalStats := tt.Snapshot()
	if finalStats.ActiveConns != 0 {
		t.Fatalf("expected 0 active connections after CloseIdleConnections(), got %d", finalStats.ActiveConns)
	}
	if finalStats.ClosedConns != 1 {
		t.Fatalf("expected 1 closed connection after CloseIdleConnections(), got %d", finalStats.ClosedConns)
	}
}
