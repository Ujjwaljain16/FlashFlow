package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"flashflow/internal/tcp"
)

// measureDialOnly dials addr n times serially and returns sorted dial latencies.
// The connection is closed immediately — no data is transferred.
// This isolates the 3-way TCP handshake cost alone.
//
// n is deliberately kept small (≤200) to stay within Windows ephemeral port
// budget. The TIME_WAIT phenomenon is documented as a finding, not hidden.
func measureDialOnly(addr string, n int) ([]time.Duration, int) {
	latencies := make([]time.Duration, 0, n)
	failures := 0
	for i := 0; i < n; i++ {
		start := time.Now()
		conn, err := net.Dial("tcp", addr)
		elapsed := time.Since(start)
		if err != nil {
			failures++
			continue
		}
		conn.Close()
		latencies = append(latencies, elapsed)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return latencies, failures
}

type latencyStats struct {
	P50 time.Duration `json:"p50_ns"`
	P95 time.Duration `json:"p95_ns"`
	P99 time.Duration `json:"p99_ns"`
	Min time.Duration `json:"min_ns"`
	Max time.Duration `json:"max_ns"`
}

func statsFrom(sorted []time.Duration) latencyStats {
	if len(sorted) == 0 {
		return latencyStats{}
	}
	n := len(sorted)
	return latencyStats{
		Min: sorted[0],
		Max: sorted[n-1],
		P50: sorted[n*50/100],
		P95: sorted[n*95/100],
		P99: sorted[n*99/100],
	}
}

type decompositionResult struct {
	Experiment  string `json:"experiment"`
	Concurrency int    `json:"concurrency"`
	Requests    int    `json:"requests"`
	PayloadSize int    `json:"payload_bytes"`

	// A: dial-only (no data transferred)
	DialOnlySuccessful int          `json:"dial_only_successful"`
	DialOnlyFailed     int          `json:"dial_only_failed"`
	DialOnly           latencyStats `json:"dial_only"`

	// B: per-request — dial + write + read + close
	// ConnLatency is the dial portion; RTT is the write→read portion.
	PerRequestConnLatency latencyStats `json:"per_request_conn_latency"`
	PerRequestRTTLatency  latencyStats `json:"per_request_rtt_latency"`
	PerRequestSuccessful  int          `json:"per_request_successful"`
	PerRequestFailed      int          `json:"per_request_failed"`

	// C: persistent — pre-established connection, write + read only
	PersistentRTTLatency latencyStats `json:"persistent_rtt_latency"`
	PersistentSuccessful int          `json:"persistent_successful"`
	PersistentFailed     int          `json:"persistent_failed"`
}

// Cooldown pauses to let TIME_WAIT sockets begin draining.
// Note: this does NOT eliminate TIME_WAIT (which lasts ~60s on Windows).
// It prevents the next phase from immediately inheriting the previous phase's
// maximum congestion. The documentation records this limitation explicitly.
func cooldown(label string, d time.Duration) {
	fmt.Printf("  [cooldown %v after %s]\n", d, label)
	time.Sleep(d)
}

func main() {
	outDir := filepath.Join("experiments", "001-tcp-connection-lifecycle", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	// Test cases: kept conservative on requests to avoid complete port exhaustion.
	// c=100 is included intentionally — the failure mode IS the finding.
	testCases := []struct {
		concurrency int
		requests    int
		payload     int
		// dialN: separate cap for phase A (dial-only).
		// Serial dial-close burns ephemeral ports fast; cap < 200 on Windows.
		dialN int
	}{
		{1, 200, 64, 200},
		{10, 500, 64, 150},
		{100, 500, 64, 100}, // port exhaustion expected in phase A; document it
	}

	for _, tc := range testCases {
		fmt.Printf("\n=== Experiment 001-A  c=%-3d r=%-5d p=%-4d dialN=%-3d ===\n",
			tc.concurrency, tc.requests, tc.payload, tc.dialN)

		payloadBytes := bytes.Repeat([]byte("X"), tc.payload)

		server := tcp.NewServer("127.0.0.1:0")
		if err := server.Start(); err != nil {
			log.Fatalf("failed to start server: %v", err)
		}
		addr := server.AddrPort()

		// Warmup with persistent only (avoids burning ports before measurements)
		wCfg := tcp.ClientConfig{
			Addr: addr, Requests: 50,
			Concurrency: tc.concurrency, Payload: payloadBytes,
			ConnectionMode: "persistent",
		}
		tcp.RunBenchmark(wCfg)
		cooldown("warmup", 200*time.Millisecond)

		// --- Phase A: Dial-only ---
		fmt.Printf("  A: dial-only (%d serial dials)...\n", tc.dialN)
		dialLatencies, dialFailed := measureDialOnly(addr, tc.dialN)
		dialStats := statsFrom(dialLatencies)
		fmt.Printf("     successful=%d failed=%d  p50=%v p99=%v\n",
			len(dialLatencies), dialFailed, dialStats.P50, dialStats.P99)

		cooldown("phase-A", 500*time.Millisecond)

		// --- Phase B: Per-request ---
		fmt.Printf("  B: per-request transaction (c=%d r=%d)...\n", tc.concurrency, tc.requests)
		bCfg := tcp.ClientConfig{
			Addr: addr, Requests: tc.requests,
			Concurrency: tc.concurrency, Payload: payloadBytes,
			ConnectionMode: "per-request",
		}
		bRes, _ := tcp.RunBenchmark(bCfg)
		fmt.Printf("     successful=%d failed=%d  connP50=%v rttP50=%v\n",
			bRes.SuccessfulRequests, bRes.FailedRequests,
			bRes.P50ConnLatency, bRes.P50ReqLatency)

		cooldown("phase-B", 500*time.Millisecond)

		// --- Phase C: Persistent ---
		fmt.Printf("  C: persistent RTT (c=%d r=%d)...\n", tc.concurrency, tc.requests)
		cCfg := tcp.ClientConfig{
			Addr: addr, Requests: tc.requests,
			Concurrency: tc.concurrency, Payload: payloadBytes,
			ConnectionMode: "persistent",
		}
		cRes, _ := tcp.RunBenchmark(cCfg)
		fmt.Printf("     successful=%d failed=%d  rttP50=%v\n",
			cRes.SuccessfulRequests, cRes.FailedRequests, cRes.P50ReqLatency)

		// Stop server
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		server.Stop(stopCtx)
		cancel()
		cooldown("post-cell", 300*time.Millisecond)

		// Record
		result := decompositionResult{
			Experiment:  "001-A-connection-cost-decomposition",
			Concurrency: tc.concurrency, Requests: tc.requests, PayloadSize: tc.payload,

			DialOnlySuccessful: len(dialLatencies),
			DialOnlyFailed:     dialFailed,
			DialOnly:           dialStats,

			PerRequestConnLatency: latencyStats{
				P50: bRes.P50ConnLatency, P95: bRes.P95ConnLatency, P99: bRes.P99ConnLatency,
			},
			PerRequestRTTLatency: latencyStats{
				P50: bRes.P50ReqLatency, P95: bRes.P95ReqLatency, P99: bRes.P99ReqLatency,
				Min: bRes.MinReqLatency, Max: bRes.MaxReqLatency,
			},
			PerRequestSuccessful: bRes.SuccessfulRequests,
			PerRequestFailed:     bRes.FailedRequests,

			PersistentRTTLatency: latencyStats{
				P50: cRes.P50ReqLatency, P95: cRes.P95ReqLatency, P99: cRes.P99ReqLatency,
				Min: cRes.MinReqLatency, Max: cRes.MaxReqLatency,
			},
			PersistentSuccessful: cRes.SuccessfulRequests,
			PersistentFailed:     cRes.FailedRequests,
		}

		filename := filepath.Join(outDir,
			fmt.Sprintf("001A-c%03d-r%05d.json", tc.concurrency, tc.requests))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(filename, b, 0644)

		// Inline comparison table
		fmt.Printf("\n  Causal decomposition  c=%-3d r=%-5d p=%d bytes\n",
			tc.concurrency, tc.requests, tc.payload)
		fmt.Printf("  %-38s  p50=%-12v  p99=%-12v\n",
			"A: Dial only (3-way handshake)", dialStats.P50, dialStats.P99)
		fmt.Printf("  %-38s  p50=%-12v  p99=%-12v\n",
			"B: Per-req conn latency (dial part)", bRes.P50ConnLatency, bRes.P99ConnLatency)
		fmt.Printf("  %-38s  p50=%-12v  p99=%-12v\n",
			"B: Per-req app RTT (excl. dial)", bRes.P50ReqLatency, bRes.P99ReqLatency)
		fmt.Printf("  %-38s  p50=%-12v  p99=%-12v\n",
			"C: Persistent app RTT (no dial)", cRes.P50ReqLatency, cRes.P99ReqLatency)
		fmt.Printf("  Note: phase-A dial failures=%d (TIME_WAIT / port exhaustion)\n", dialFailed)
	}

	fmt.Println("\nExperiment 001-A complete.")
}
