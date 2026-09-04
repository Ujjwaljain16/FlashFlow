package tcp

import (
	"bytes"
	"net"
	"sort"
	"sync/atomic"
	"time"
)

// ClientResult aggregates the outcome of a RunBenchmark call.
//
// Latency definitions:
//   - ConnectLatency: wall-clock time to complete net.Dial() (per-request mode only).
//   - RequestLatency: wall-clock time from completing the write to receiving the
//     full response. This excludes connection establishment.
//   - TransactionLatency: ConnectLatency + RequestLatency.
//     In persistent mode this equals RequestLatency (no dial per request).
//
// Percentiles use sorted nearest-rank index selection:
//
//	p50 = latencies[n*50/100]
//
// This is a simple approximation; it will be replaced by a proper histogram
// implementation in a later stage when we need coordinated-omission-safe
// measurements.
type ClientResult struct {
	TotalRequests      int           `json:"requests"`
	SuccessfulRequests int           `json:"successful"`
	FailedRequests     int           `json:"failed"`
	TotalDuration      time.Duration `json:"total_duration_ns"`
	ConnectionsMade    int           `json:"connections"`

	// Application request RTT (excludes dial in persistent mode).
	P50ReqLatency time.Duration `json:"p50_req_ns"`
	P95ReqLatency time.Duration `json:"p95_req_ns"`
	P99ReqLatency time.Duration `json:"p99_req_ns"`
	MinReqLatency time.Duration `json:"min_req_ns"`
	MaxReqLatency time.Duration `json:"max_req_ns"`

	// TCP connection establishment (populated in per-request mode only).
	P50ConnLatency time.Duration `json:"p50_conn_ns,omitempty"`
	P95ConnLatency time.Duration `json:"p95_conn_ns,omitempty"`
	P99ConnLatency time.Duration `json:"p99_conn_ns,omitempty"`

	ThroughputRPS float64 `json:"throughput_rps"`
	PayloadBytes  int64   `json:"payload_bytes"`
}

// ClientConfig holds benchmark settings.
type ClientConfig struct {
	Addr           string
	Requests       int
	Concurrency    int
	Payload        []byte
	ConnectionMode string // "persistent" or "per-request"
}

// workerRequests distributes total requests across concurrency workers,
// handling the remainder so that sum(result) == total.
func workerRequests(total, concurrency int) []int {
	base := total / concurrency
	rem := total % concurrency
	out := make([]int, concurrency)
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// RunBenchmark executes the benchmark and returns aggregated results.
func RunBenchmark(cfg ClientConfig) (ClientResult, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}

	dist := workerRequests(cfg.Requests, cfg.Concurrency)

	type sample struct {
		req  time.Duration // request RTT only
		conn time.Duration // connection establishment (zero for persistent)
	}

	sampleCh := make(chan sample, cfg.Requests)

	var successCount atomic.Int64
	var failCount atomic.Int64
	var connsMade atomic.Int64

	var workersDone = make(chan struct{})
	pending := int64(cfg.Concurrency)

	start := time.Now()

	for i := 0; i < cfg.Concurrency; i++ {
		go func(workerReqs int) {
			defer func() {
				if atomic.AddInt64(&pending, -1) == 0 {
					close(workersDone)
				}
			}()

			if cfg.ConnectionMode == "persistent" {
				conn, err := net.Dial("tcp", cfg.Addr)
				if err != nil {
					failCount.Add(int64(workerReqs))
					return
				}
				connsMade.Add(1)
				defer conn.Close()

				for j := 0; j < workerReqs; j++ {
					reqStart := time.Now()
					err = WriteMessage(conn, cfg.Payload)
					if err != nil {
						failCount.Add(1)
						// Connection is broken — reconnect for remaining requests.
						conn.Close()
						conn, err = net.Dial("tcp", cfg.Addr)
						if err != nil {
							failCount.Add(int64(workerReqs - j - 1))
							return
						}
						connsMade.Add(1)
						continue
					}

					resp, err := ReadMessage(conn)
					if err != nil || !bytes.Equal(resp, cfg.Payload) {
						failCount.Add(1)
						// Connection is broken — reconnect.
						conn.Close()
						conn, err = net.Dial("tcp", cfg.Addr)
						if err != nil {
							failCount.Add(int64(workerReqs - j - 1))
							return
						}
						connsMade.Add(1)
						continue
					}

					successCount.Add(1)
					sampleCh <- sample{req: time.Since(reqStart)}
				}

			} else { // per-request
				for j := 0; j < workerReqs; j++ {
					// Measure dial separately so we can report it independently.
					dialStart := time.Now()
					conn, err := net.Dial("tcp", cfg.Addr)
					connLatency := time.Since(dialStart)

					if err != nil {
						failCount.Add(1)
						continue
					}
					connsMade.Add(1)

					reqStart := time.Now()
					err = WriteMessage(conn, cfg.Payload)
					if err != nil {
						failCount.Add(1)
						conn.Close()
						continue
					}

					resp, err := ReadMessage(conn)
					conn.Close()

					if err != nil || !bytes.Equal(resp, cfg.Payload) {
						failCount.Add(1)
						continue
					}

					successCount.Add(1)
					sampleCh <- sample{req: time.Since(reqStart), conn: connLatency}
				}
			}
		}(dist[i])
	}

	<-workersDone
	close(sampleCh)
	totalDur := time.Since(start)

	reqLatencies := make([]time.Duration, 0, successCount.Load())
	connLatencies := make([]time.Duration, 0)

	for s := range sampleCh {
		reqLatencies = append(reqLatencies, s.req)
		if s.conn > 0 {
			connLatencies = append(connLatencies, s.conn)
		}
	}

	sort.Slice(reqLatencies, func(i, j int) bool { return reqLatencies[i] < reqLatencies[j] })
	sort.Slice(connLatencies, func(i, j int) bool { return connLatencies[i] < connLatencies[j] })

	sc := int(successCount.Load())
	res := ClientResult{
		TotalRequests:      cfg.Requests,
		SuccessfulRequests: sc,
		FailedRequests:     int(failCount.Load()),
		TotalDuration:      totalDur,
		ConnectionsMade:    int(connsMade.Load()),
		ThroughputRPS:      float64(sc) / totalDur.Seconds(),
		PayloadBytes:       int64(sc) * int64(len(cfg.Payload)),
	}

	if len(reqLatencies) > 0 {
		n := len(reqLatencies)
		res.MinReqLatency = reqLatencies[0]
		res.MaxReqLatency = reqLatencies[n-1]
		res.P50ReqLatency = reqLatencies[n*50/100]
		res.P95ReqLatency = reqLatencies[n*95/100]
		res.P99ReqLatency = reqLatencies[n*99/100]
	}

	if len(connLatencies) > 0 {
		n := len(connLatencies)
		res.P50ConnLatency = connLatencies[n*50/100]
		res.P95ConnLatency = connLatencies[n*95/100]
		res.P99ConnLatency = connLatencies[n*99/100]
	}

	return res, nil
}
