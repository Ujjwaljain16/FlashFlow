package httpx

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"flashflow/internal/transport"
)

// BenchmarkConfig defines the workload parameters for an HTTP benchmark run.
type BenchmarkConfig struct {
	TargetURL       string                    `json:"target_url"`
	Requests        int                       `json:"requests"`
	Concurrency     int                       `json:"concurrency"`
	Payload         []byte                    `json:"payload"`
	Method          string                    `json:"method"`
	Path            string                    `json:"path"`
	ClientTransport transport.TransportConfig `json:"client_transport"`
	Timeout         time.Duration             `json:"timeout"`
	// PathFunc, if set, is called once per request to produce that
	// request's path, overriding Path — for workloads that hit more than
	// one key (e.g. a hot/cold key-access pattern for cache experiments).
	// Called concurrently from every worker goroutine; thread safety is
	// the caller's responsibility.
	PathFunc func() string `json:"-"`
}

// LatencyPercentiles summarizes measured latency distribution.
type LatencyPercentiles struct {
	P50 time.Duration `json:"p50"`
	P95 time.Duration `json:"p95"`
	P99 time.Duration `json:"p99"`
	Min time.Duration `json:"min"`
	Max time.Duration `json:"max"`
}

// BenchmarkResult stores the outcomes of an HTTP load test run.
type BenchmarkResult struct {
	TotalRequests      int                `json:"total_requests"`
	SuccessfulRequests int                `json:"successful_requests"`
	FailedRequests     int                `json:"failed_requests"`
	TotalDuration      time.Duration      `json:"total_duration"`
	ThroughputRPS      float64            `json:"throughput_rps"`
	ClientLatencies    LatencyPercentiles `json:"client_latencies"`
	ClientDials        uint64             `json:"client_dials"`
}

func calculatePercentiles(sorted []time.Duration) LatencyPercentiles {
	if len(sorted) == 0 {
		return LatencyPercentiles{}
	}
	n := len(sorted)
	return LatencyPercentiles{
		Min: sorted[0],
		Max: sorted[n-1],
		P50: sorted[n*50/100],
		P95: sorted[n*95/100],
		P99: sorted[n*99/100],
	}
}

// RunHTTPBenchmark executes a concurrent HTTP benchmark against targetURL.
func RunHTTPBenchmark(cfg BenchmarkConfig) (BenchmarkResult, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.Requests <= 0 {
		cfg.Requests = 10
	}
	if cfg.Method == "" {
		cfg.Method = http.MethodGet
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}

	tCfg := cfg.ClientTransport
	if tCfg.Label == "" {
		tCfg.Label = "benchmark_client"
	}
	tt := transport.NewTrackedTransport(tCfg)
	client := tt.HTTPClient(cfg.Timeout)

	var (
		wg           sync.WaitGroup
		successCount atomic.Uint64
		failCount    atomic.Uint64
		mu           sync.Mutex
		latencies    = make([]time.Duration, 0, cfg.Requests)
	)

	baseReqs := cfg.Requests / cfg.Concurrency
	rem := cfg.Requests % cfg.Concurrency

	staticReqURL := cfg.TargetURL
	if cfg.Path != "" {
		staticReqURL += cfg.Path
	}

	start := time.Now()

	for workerID := 0; workerID < cfg.Concurrency; workerID++ {
		workerReqs := baseReqs
		if workerID < rem {
			workerReqs++
		}
		if workerReqs == 0 {
			continue
		}

		wg.Add(1)
		go func(count int) {
			defer wg.Done()
			localLats := make([]time.Duration, 0, count)

			for i := 0; i < count; i++ {
				var body io.Reader
				if len(cfg.Payload) > 0 {
					body = bytes.NewReader(cfg.Payload)
				}

				reqURL := staticReqURL
				if cfg.PathFunc != nil {
					reqURL = cfg.TargetURL + cfg.PathFunc()
				}

				req, err := http.NewRequestWithContext(context.Background(), cfg.Method, reqURL, body)
				if err != nil {
					failCount.Add(1)
					continue
				}
				req.Header.Set("Content-Type", "application/octet-stream")

				reqStart := time.Now()
				resp, err := client.Do(req)
				elapsed := time.Since(reqStart)

				if err != nil {
					failCount.Add(1)
					continue
				}

				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()

				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					successCount.Add(1)
					localLats = append(localLats, elapsed)
				} else {
					failCount.Add(1)
				}
			}

			mu.Lock()
			latencies = append(latencies, localLats...)
			mu.Unlock()
		}(workerReqs)
	}

	wg.Wait()
	totalDuration := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	succ := int(successCount.Load())
	fail := int(failCount.Load())

	var rps float64
	if totalDuration > 0 {
		rps = float64(succ) / totalDuration.Seconds()
	}

	tt.CloseIdleConnections()

	return BenchmarkResult{
		TotalRequests:      cfg.Requests,
		SuccessfulRequests: succ,
		FailedRequests:     fail,
		TotalDuration:      totalDuration,
		ThroughputRPS:      rps,
		ClientLatencies:    calculatePercentiles(latencies),
		ClientDials:        tt.Snapshot().SuccessfulDials,
	}, nil
}
