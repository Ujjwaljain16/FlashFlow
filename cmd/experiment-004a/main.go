package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/httpx"
	"flashflow/internal/proxy"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

const outDirName = "experiments/004-caching-failures/results"

// keyWeights defines the hot/cold key-access pattern shared by every
// Stage 4 caching experiment that needs one: one hot key gets half of all
// traffic (weight 8 of a total weight 16), the other half is split evenly
// across 8 cold keys (weight 1 each). Reuses Stage 3's
// WeightedRoundRobinSelector as a workload generator rather than writing a
// second weighted-random-pick implementation — the smooth-interleaving
// property that made it the right fit for routing (Experiment 003-B) is
// exactly what avoids an artificially bursty access pattern here too.
func keyWeights() (keys []string, weights proxy.TargetWeights) {
	keys = []string{"/data/hot"}
	weights = proxy.TargetWeights{"/data/hot": 8}
	for i := 0; i < 8; i++ {
		k := fmt.Sprintf("/data/cold-%d", i)
		keys = append(keys, k)
		weights[k] = 1
	}
	return keys, weights
}

type Experiment004AResult struct {
	Experiment            string                   `json:"experiment"`
	Timestamp             string                   `json:"timestamp"`
	Concurrency           int                      `json:"concurrency"`
	Requests              int                      `json:"requests"`
	OriginDelayMs         int                      `json:"origin_delay_ms"`
	HotKeySharePercent    float64                  `json:"hot_key_share_percent_configured"`
	SuccessfulRequests    int                      `json:"successful_requests"`
	FailedRequests        int                      `json:"failed_requests"`
	ThroughputRPS         float64                  `json:"throughput_rps"`
	ClientLatencies       httpx.LatencyPercentiles `json:"client_latencies"`
	EdgeOriginDials       uint64                   `json:"edge_origin_dials"`
	EdgeOriginRequests    uint64                   `json:"edge_origin_requests"`
	UpstreamEqualsSuccess bool                     `json:"upstream_equals_success"`
	Findings              string                   `json:"findings"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-A: No-Cache Baseline (Hot/Cold Key Workload)")
	fmt.Println(" Topology: Client -> Edge (no cache yet) -> Origin (15ms processing delay)")
	fmt.Println("==========================================================================================")

	const (
		concurrency = 30
		requests    = 3000
		originDelay = 15 * time.Millisecond
	)

	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-004a", DefaultDelay: originDelay})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-004a",
		OriginURL: origin.URL(),
		TransportConfig: transport.TransportConfig{
			Label: "edge_origin_004a", MaxIdleConnsPerHost: 100, MaxIdleConns: 300,
		},
	})
	if err != nil {
		log.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		log.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	keys, weights := keyWeights()
	keyGen := proxy.NewWeightedRoundRobinSelector(weights)
	pathFunc := func() string {
		k, _ := keyGen.SelectTarget(nil, keys)
		return k
	}

	// Warmup: discarded, and deliberately uses a static path rather than
	// pathFunc so it doesn't consume any of the key generator's smooth
	// rotation state before the measured run begins (see Experiment
	// 003-A's warmup-contamination lesson — same principle, different
	// kind of state: there a benchmark counter, here a WRR key cycle).
	_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL: edge.URL(), Path: "/data/warmup", Requests: 30, Concurrency: concurrency,
	})
	time.Sleep(100 * time.Millisecond)

	baselineDials := edge.TransportStats().SuccessfulDials
	baselineReqs := edge.TransportStats().RequestsCompleted

	res, err := httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL:   edge.URL(),
		Requests:    requests,
		Concurrency: concurrency,
		PathFunc:    pathFunc,
	})
	if err != nil {
		log.Fatalf("benchmark failed: %v", err)
	}

	edgeDials := edge.TransportStats().SuccessfulDials - baselineDials
	edgeReqs := edge.TransportStats().RequestsCompleted - baselineReqs

	upstreamEqualsSuccess := int(edgeReqs) == res.SuccessfulRequests
	if !upstreamEqualsSuccess {
		log.Fatalf("measurement contamination detected: edge-forwarded requests (%d) do not match "+
			"client-successful requests (%d) — H1's core prediction cannot be evaluated on contaminated data",
			edgeReqs, res.SuccessfulRequests)
	}

	finding := fmt.Sprintf(
		"No-Cache baseline: %d/%d requests succeeded, RPS=%.1f, p50=%v p95=%v p99=%v. "+
			"Upstream (edge->origin) requests=%d, exactly equal to successful client requests: %t — "+
			"confirms every request reaches Origin with no caching present, as H1 predicted.",
		res.SuccessfulRequests, res.TotalRequests, res.ThroughputRPS,
		res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99,
		edgeReqs, upstreamEqualsSuccess,
	)

	result := Experiment004AResult{
		Experiment:            "004-A-no-cache-baseline",
		Timestamp:             time.Now().UTC().Format(time.RFC3339),
		Concurrency:           concurrency,
		Requests:              requests,
		OriginDelayMs:         int(originDelay.Milliseconds()),
		HotKeySharePercent:    50.0,
		SuccessfulRequests:    res.SuccessfulRequests,
		FailedRequests:        res.FailedRequests,
		ThroughputRPS:         res.ThroughputRPS,
		ClientLatencies:       res.ClientLatencies,
		EdgeOriginDials:       edgeDials,
		EdgeOriginRequests:    edgeReqs,
		UpstreamEqualsSuccess: upstreamEqualsSuccess,
		Findings:              finding,
	}

	fname := filepath.Join(outDirName, "004A-no-cache-baseline.json")
	b, _ := json.MarshalIndent(result, "", "  ")
	os.WriteFile(fname, b, 0644)

	fmt.Printf("  Results: RPS=%.1f | p50=%v | p95=%v | p99=%v\n",
		res.ThroughputRPS, res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
	fmt.Printf("  Edge->Origin: dials=%d requests=%d (client successes=%d, equal=%t)\n",
		edgeDials, edgeReqs, res.SuccessfulRequests, upstreamEqualsSuccess)
	fmt.Println("\nExperiment 004-A (No-Cache baseline) complete.")
}
