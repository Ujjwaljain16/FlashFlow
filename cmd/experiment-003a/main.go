package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/httpx"
	"flashflow/internal/proxy"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

// edgeCount captures both independent measurements of how many requests one
// edge handled: the proxy's view (health registry TotalAppRequests, recorded
// every time ServeHTTP receives an upstream response or error for this
// target) and the edge's own view (its edge->origin TrackedTransport
// RequestsCompleted, recorded every time the edge successfully forwards to
// Origin). These are independent instrumentation points; for a healthy edge
// with no retries, they must match exactly.
type edgeCount struct {
	Instance           string `json:"instance"`
	URL                string `json:"url"`
	ProxyRecordedCount uint64 `json:"proxy_recorded_count"`
	EdgeForwardedCount uint64 `json:"edge_forwarded_count"`
	CountsAgree        bool   `json:"counts_agree"`
}

// Experiment003AResult is the on-disk record for one measured cell.
type Experiment003AResult struct {
	Experiment           string                   `json:"experiment"`
	Timestamp            string                   `json:"timestamp"`
	Concurrency          int                      `json:"concurrency"`
	Requests             int                      `json:"requests"`
	EdgeDelayMs          int                      `json:"edge_delay_ms"`
	SuccessfulRequests   int                      `json:"successful_requests"`
	FailedRequests       int                      `json:"failed_requests"`
	ThroughputRPS        float64                  `json:"throughput_rps"`
	ClientLatencies      httpx.LatencyPercentiles `json:"client_latencies"`
	PerEdgeCounts        []edgeCount              `json:"per_edge_counts"`
	IdealCountPerEdge    float64                  `json:"ideal_count_per_edge"`
	MaxMinCountDiff      uint64                   `json:"max_min_count_diff"`
	CoefficientVariation float64                  `json:"coefficient_of_variation"`
	// ClientServerOutcomeMismatch counts requests the proxy fully processed
	// and recorded (per PerEdgeCounts) but which the benchmark client
	// harness nonetheless classified as failed — see the comment at the
	// invariant check in main() for why this is tracked separately from
	// routing fairness.
	ClientServerOutcomeMismatch int    `json:"client_server_outcome_mismatch"`
	Findings                    string `json:"findings"`
}

func main() {
	outDir := filepath.Join("experiments", "003-routing-policies", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 003-A: Round Robin Fairness Baseline (Homogeneous Edges)")
	fmt.Println(" Topology: Client -> Proxy [RoundRobin] -> [Edge A, Edge B, Edge C] (all 1ms) -> Origin")
	fmt.Println("==========================================================================================")

	const edgeDelay = 1 * time.Millisecond

	testCases := []struct {
		concurrency int
		requests    int
	}{
		{concurrency: 1, requests: 300},
		{concurrency: 10, requests: 1500},
		{concurrency: 50, requests: 3000},
		{concurrency: 100, requests: 6000},
		// Deliberately NOT a multiple of 3, at high concurrency: 300, 1500,
		// 3000, and 6000 all divide evenly by len(edges)==3, so a perfect
		// max-min=0 split in those cells is a mathematical certainty of the
		// counter's modulo arithmetic, not empirical evidence that the
		// atomic selection counter holds up under concurrent contention.
		// This cell tests the actual open question: under 97-way concurrent
		// access, does `atomic.Uint64.Add` still yield a clean ceil/floor
		// split (max-min <= 1), or does contention produce a larger skew?
		{concurrency: 97, requests: 5000},
	}

	for _, tc := range testCases {
		fmt.Printf("\n--- Cell: c=%-3d r=%-4d ---\n", tc.concurrency, tc.requests)

		origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003a"})
		if err := origin.Start(); err != nil {
			log.Fatalf("failed to start origin: %v", err)
		}

		edgeNames := []string{"edge-a", "edge-b", "edge-c"}
		edges := make([]*topology.EdgeServer, 0, len(edgeNames))
		var targets []string
		for _, name := range edgeNames {
			e, err := topology.NewEdgeServer(topology.EdgeConfig{
				Instance:     name,
				OriginURL:    origin.URL(),
				DefaultDelay: edgeDelay,
				TransportConfig: transport.TransportConfig{
					Label: fmt.Sprintf("edge_origin_%s", name),
				},
			})
			if err != nil {
				log.Fatalf("failed to create %s: %v", name, err)
			}
			if err := e.Start(); err != nil {
				log.Fatalf("failed to start %s: %v", name, err)
			}
			edges = append(edges, e)
			targets = append(targets, e.URL())
		}

		clk := clock.NewWallClock()
		proxyCfg := proxy.Config{
			Targets: targets,
			TransportConfig: transport.TransportConfig{
				Label:               "proxy_upstream",
				MaxIdleConnsPerHost: 100,
				MaxIdleConns:        300,
			},
			HealthConfig: health.DefaultConfig(),
			ProberConfig: health.DefaultCheckerConfig(),
		}
		pxy := proxy.NewReverseProxy(proxyCfg, clk, proxy.NewRoundRobinSelector())
		if err := pxy.Start(); err != nil {
			log.Fatalf("failed to start proxy: %v", err)
		}

		payloadBytes := bytes.Repeat([]byte("R"), 64)

		// Warmup run (discarded) so the connection pools and health prober
		// are settled before the measured run begins.
		wCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    30,
			Concurrency: tc.concurrency,
			Payload:     payloadBytes,
		}
		_, _ = httpx.RunHTTPBenchmark(wCfg)
		time.Sleep(100 * time.Millisecond)

		// Baseline per-edge counters after warmup but before the measured
		// run, so warmup traffic is excluded from the fairness measurement
		// below (warmup hits the same instrumented proxy/edges — their
		// counters are cumulative and are not reset between phases).
		baselineReg := pxy.Registry().Snapshot()
		baselineEdge := make(map[string]uint64, len(edges))
		for _, e := range edges {
			baselineEdge[e.URL()] = e.TransportStats().RequestsCompleted
		}

		mCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    tc.requests,
			Concurrency: tc.concurrency,
			Payload:     payloadBytes,
		}
		res, err := httpx.RunHTTPBenchmark(mCfg)
		if err != nil {
			log.Fatalf("benchmark failed: %v", err)
		}

		regSnapshot := pxy.Registry().Snapshot()

		counts := make([]edgeCount, 0, len(edges))
		var total uint64
		countVals := make([]float64, 0, len(edges))
		for _, e := range edges {
			proxyCount := regSnapshot[e.URL()].TotalAppRequests - baselineReg[e.URL()].TotalAppRequests
			edgeCount_ := e.TransportStats().RequestsCompleted - baselineEdge[e.URL()]
			counts = append(counts, edgeCount{
				Instance:           e.URL(),
				URL:                e.URL(),
				ProxyRecordedCount: proxyCount,
				EdgeForwardedCount: edgeCount_,
				CountsAgree:        proxyCount == edgeCount_,
			})
			total += proxyCount
			countVals = append(countVals, float64(proxyCount))
		}
		// Attach human-readable instance names (edge.Instance is not exported
		// as a field; use the known ordering instead).
		for i := range counts {
			counts[i].Instance = edgeNames[i]
		}

		ideal := float64(total) / float64(len(edges))
		var minC, maxC uint64 = counts[0].ProxyRecordedCount, counts[0].ProxyRecordedCount
		for _, c := range counts {
			if c.ProxyRecordedCount < minC {
				minC = c.ProxyRecordedCount
			}
			if c.ProxyRecordedCount > maxC {
				maxC = c.ProxyRecordedCount
			}
		}

		mean := ideal
		var variance float64
		for _, v := range countVals {
			d := v - mean
			variance += d * d
		}
		variance /= float64(len(countVals))
		stddev := math.Sqrt(variance)
		cv := 0.0
		if mean > 0 {
			cv = stddev / mean
		}

		allAgree := true
		for _, c := range counts {
			if !c.CountsAgree {
				allAgree = false
			}
		}
		// The real correctness invariant is that every request the benchmark
		// client attempted was either observed as successful or as failed,
		// and the server-side (proxy) recorded exactly that many app
		// results for a target. If `total` diverges from
		// successful+failed, requests were lost or double-counted somewhere
		// in the pipeline — that is genuine contamination worth stopping
		// on. `total` diverging from `SuccessfulRequests` ALONE is a
		// weaker, non-fatal signal: it means at least one request that the
		// proxy/edge/origin fully processed (and therefore recorded) was
		// nonetheless classified as failed by the client benchmark harness
		// — most plausibly a transient client-side response-read/connection
		// error rather than an application or routing fault. Record it, do
		// not hide it, and do not treat it as a routing-fairness defect.
		if int(total) != res.SuccessfulRequests+res.FailedRequests {
			log.Fatalf("measurement contamination detected: per-edge total (%d) does not match "+
				"benchmark successful+failed requests (%d+%d) for cell c=%d r=%d — fairness data would be invalid",
				total, res.SuccessfulRequests, res.FailedRequests, tc.concurrency, tc.requests)
		}
		clientServerMismatch := int(total) - res.SuccessfulRequests

		finding := fmt.Sprintf(
			"Round Robin distributed %d requests across %d homogeneous edges with a max-min spread of %d "+
				"(coefficient of variation %.4f) against an ideal of %.1f/edge. Proxy-recorded and edge-forwarded "+
				"counts agreed for all edges: %t. Client-vs-server outcome mismatch: %d "+
				"(requests the proxy fully processed but the client benchmark harness recorded as failed).",
			total, len(edges), maxC-minC, cv, ideal, allAgree, clientServerMismatch,
		)

		result := Experiment003AResult{
			Experiment:                  "003-A-round-robin-fairness-baseline",
			Timestamp:                   time.Now().UTC().Format(time.RFC3339),
			Concurrency:                 tc.concurrency,
			Requests:                    tc.requests,
			EdgeDelayMs:                 int(edgeDelay.Milliseconds()),
			SuccessfulRequests:          res.SuccessfulRequests,
			FailedRequests:              res.FailedRequests,
			ThroughputRPS:               res.ThroughputRPS,
			ClientLatencies:             res.ClientLatencies,
			PerEdgeCounts:               counts,
			IdealCountPerEdge:           ideal,
			MaxMinCountDiff:             maxC - minC,
			CoefficientVariation:        cv,
			ClientServerOutcomeMismatch: clientServerMismatch,
			Findings:                    finding,
		}

		fname := filepath.Join(outDir, fmt.Sprintf("003A-c%03d-r%04d.json", tc.concurrency, tc.requests))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(fname, b, 0644)

		fmt.Printf("  Results: RPS=%.1f | p50=%v | p95=%v | p99=%v\n",
			res.ThroughputRPS, res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
		for _, c := range counts {
			fmt.Printf("  %-8s proxy=%-6d edge_forwarded=%-6d agree=%t\n",
				c.Instance, c.ProxyRecordedCount, c.EdgeForwardedCount, c.CountsAgree)
		}
		fmt.Printf("  Fairness: ideal=%.1f max-min=%d CV=%.4f\n", ideal, maxC-minC, cv)
		if clientServerMismatch != 0 {
			fmt.Printf("  NOTE: client/server outcome mismatch=%d (server fully processed but client marked failed)\n",
				clientServerMismatch)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pxy.Stop(ctx)
		for _, e := range edges {
			e.Stop(ctx)
		}
		origin.Stop(ctx)
		cancel()
		time.Sleep(150 * time.Millisecond)
	}

	fmt.Println("\nExperiment 003-A complete.")
}
