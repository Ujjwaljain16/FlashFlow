package tcp

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestWorkerRequests_Distribution(t *testing.T) {
	tests := []struct {
		total, concurrency int
	}{
		{1000, 10},  // evenly divisible
		{1001, 10},  // remainder 1
		{1005, 100}, // remainder 5
		{7, 3},      // 3, 2, 2
	}
	for _, tt := range tests {
		dist := workerRequests(tt.total, tt.concurrency)
		sum := 0
		for _, v := range dist {
			sum += v
		}
		if sum != tt.total {
			t.Errorf("total=%d concurrency=%d: sum of dist=%d", tt.total, tt.concurrency, sum)
		}
	}
}

func TestClient_RunBenchmark(t *testing.T) {
	server := NewServer("127.0.0.1:0")
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer server.Stop(ctx)

	payload := bytes.Repeat([]byte("test"), 8)

	tests := []struct {
		name        string
		mode        string
		concurrency int
		requests    int
	}{
		{"Single Persistent", "persistent", 1, 10},
		{"Concurrent Persistent", "persistent", 5, 53}, // 53 not divisible by 5
		{"Single Per-Request", "per-request", 1, 10},
		{"Concurrent Per-Request", "per-request", 5, 53},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ClientConfig{
				Addr:           server.AddrPort(),
				Requests:       tt.requests,
				Concurrency:    tt.concurrency,
				Payload:        payload,
				ConnectionMode: tt.mode,
			}

			res, err := RunBenchmark(cfg)
			if err != nil {
				t.Fatalf("benchmark failed: %v", err)
			}

			if res.SuccessfulRequests != tt.requests {
				t.Errorf("expected %d successful requests, got %d (failed: %d)",
					tt.requests, res.SuccessfulRequests, res.FailedRequests)
			}

			if tt.mode == "persistent" && res.ConnectionsMade != tt.concurrency {
				t.Errorf("persistent: expected %d connections, made %d", tt.concurrency, res.ConnectionsMade)
			}
			if tt.mode == "per-request" && res.ConnectionsMade != tt.requests {
				t.Errorf("per-request: expected %d connections, made %d", tt.requests, res.ConnectionsMade)
			}
			// per-request must populate conn latencies; persistent must not
			if tt.mode == "per-request" && res.P50ConnLatency == 0 {
				t.Errorf("per-request: expected P50ConnLatency > 0")
			}
			if tt.mode == "persistent" && res.P50ConnLatency != 0 {
				t.Errorf("persistent: expected P50ConnLatency == 0, got %v", res.P50ConnLatency)
			}
		})
	}
}
