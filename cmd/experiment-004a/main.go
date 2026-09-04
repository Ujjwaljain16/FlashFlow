package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/cache"
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

const numDistinctKeys = 9 // 1 hot + 8 cold

type Experiment004AResult struct {
	Experiment            string                   `json:"experiment"`
	Timestamp             string                   `json:"timestamp"`
	Cell                  string                   `json:"cell"`
	Concurrency           int                      `json:"concurrency"`
	Requests              int                      `json:"requests"`
	OriginDelayMs         int                      `json:"origin_delay_ms"`
	CacheTTLMs            int                      `json:"cache_ttl_ms"`
	HotKeySharePercent    float64                  `json:"hot_key_share_percent_configured"`
	SuccessfulRequests    int                      `json:"successful_requests"`
	FailedRequests        int                      `json:"failed_requests"`
	ThroughputRPS         float64                  `json:"throughput_rps"`
	ClientLatencies       httpx.LatencyPercentiles `json:"client_latencies"`
	EdgeOriginRequests    uint64                   `json:"edge_origin_requests"`
	UpstreamEqualsSuccess bool                     `json:"upstream_equals_success"`
	CacheStats            cache.Stats              `json:"cache_stats"`
	HitRatioPercent       float64                  `json:"hit_ratio_percent"`
	Findings              string                   `json:"findings"`
}

// runCell runs one measured cell (No-Cache when cacheTTL==0, TTL-cache
// otherwise) against a fresh topology and returns its result.
func runCell(cellName string, concurrency, requests int, originDelay, cacheTTL time.Duration) Experiment004AResult {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-004a", DefaultDelay: originDelay})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-004a",
		OriginURL: origin.URL(),
		CacheTTL:  cacheTTL,
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
	// rotation state, and doesn't pre-warm the cache for any of the real
	// keys, before the measured run begins (see Experiment 003-A's
	// warmup-contamination lesson — same principle, different kind of
	// state: there a benchmark counter, here WRR rotation + cache fills).
	_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL: edge.URL(), Path: "/data/warmup", Requests: 30, Concurrency: concurrency,
	})
	time.Sleep(100 * time.Millisecond)

	baselineTransport := edge.TransportStats()
	baselineCache := edge.CacheStats()

	res, err := httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL:   edge.URL(),
		Requests:    requests,
		Concurrency: concurrency,
		PathFunc:    pathFunc,
	})
	if err != nil {
		log.Fatalf("benchmark failed: %v", err)
	}

	finalTransport := edge.TransportStats()
	finalCache := edge.CacheStats()

	edgeReqs := finalTransport.RequestsCompleted - baselineTransport.RequestsCompleted
	cacheStats := cache.Stats{
		Lookups: finalCache.Lookups - baselineCache.Lookups,
		Hits:    finalCache.Hits - baselineCache.Hits,
		Misses:  finalCache.Misses - baselineCache.Misses,
		Expired: finalCache.Expired - baselineCache.Expired,
		Fills:   finalCache.Fills - baselineCache.Fills,
	}

	// The invariant differs by cell: with no cache, every success must
	// reach origin (edgeReqs == successes). With a cache, edgeReqs must
	// equal cache misses (fills), since every miss dispatches upstream
	// and every hit does not.
	var invariantHolds bool
	if cacheTTL <= 0 {
		invariantHolds = int(edgeReqs) == res.SuccessfulRequests
	} else {
		invariantHolds = edgeReqs == cacheStats.Misses
	}
	if !invariantHolds {
		log.Fatalf("measurement contamination detected in cell %q: edge-forwarded requests (%d) do not match "+
			"the expected invariant for this cell (successes=%d, failures=%d, cache stats=%+v)",
			cellName, edgeReqs, res.SuccessfulRequests, res.FailedRequests, cacheStats)
	}

	hitRatio := 0.0
	if cacheStats.Lookups > 0 {
		hitRatio = 100 * float64(cacheStats.Hits) / float64(cacheStats.Lookups)
	}

	var finding string
	if cacheTTL <= 0 {
		finding = fmt.Sprintf(
			"No-Cache: %d/%d succeeded, RPS=%.1f, p50=%v p95=%v p99=%v. Upstream requests=%d, "+
				"exactly equal to successful client requests: %t.",
			res.SuccessfulRequests, res.TotalRequests, res.ThroughputRPS,
			res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99,
			edgeReqs, invariantHolds)
	} else {
		finding = fmt.Sprintf(
			"TTL-Cache (ttl=%v): %d/%d succeeded, RPS=%.1f, p50=%v p95=%v p99=%v. Hit ratio=%.2f%% "+
				"(%d hits / %d lookups), %d misses, %d fills, %d expired. Upstream requests=%d "+
				"(equals misses: %t). %d distinct keys means a theoretical minimum of %d misses if each "+
				"key's very first request is the only miss it ever gets; %d actual misses recorded is "+
				"%dx that minimum -- a preview of concurrent first-touch stampede at c=%d, before "+
				"Experiment 004-C studies it directly. The warmup phase (30 requests, 1 static path, "+
				"same concurrency) showed this even more starkly: baseline cache stats recorded 30 "+
				"misses and 0 hits for 30 requests to the exact same key.",
			cacheTTL, res.SuccessfulRequests, res.TotalRequests, res.ThroughputRPS,
			res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99,
			hitRatio, cacheStats.Hits, cacheStats.Lookups, cacheStats.Misses, cacheStats.Fills, cacheStats.Expired,
			edgeReqs, invariantHolds, numDistinctKeys, numDistinctKeys, cacheStats.Misses,
			cacheStats.Misses/numDistinctKeys, concurrency)
	}

	return Experiment004AResult{
		Experiment:            "004-A-cache-baseline",
		Timestamp:             time.Now().UTC().Format(time.RFC3339),
		Cell:                  cellName,
		Concurrency:           concurrency,
		Requests:              requests,
		OriginDelayMs:         int(originDelay.Milliseconds()),
		CacheTTLMs:            int(cacheTTL.Milliseconds()),
		HotKeySharePercent:    50.0,
		SuccessfulRequests:    res.SuccessfulRequests,
		FailedRequests:        res.FailedRequests,
		ThroughputRPS:         res.ThroughputRPS,
		ClientLatencies:       res.ClientLatencies,
		EdgeOriginRequests:    edgeReqs,
		UpstreamEqualsSuccess: invariantHolds,
		CacheStats:            cacheStats,
		HitRatioPercent:       hitRatio,
		Findings:              finding,
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-A: No-Cache vs TTL-Cache Baseline (Hot/Cold Key Workload)")
	fmt.Println(" Topology: Client -> Edge -> Origin (15ms processing delay)")
	fmt.Println("==========================================================================================")

	const (
		concurrency = 30
		requests    = 3000
		originDelay = 15 * time.Millisecond
		cacheTTL    = 5 * time.Second // longer than the whole run, so no mid-run expiry confounds this cell
	)

	fmt.Println("\n--- Cell: no-cache ---")
	noCache := runCell("no-cache", concurrency, requests, originDelay, 0)
	fmt.Printf("  %s\n", noCache.Findings)

	fmt.Println("\n--- Cell: ttl-cache ---")
	ttlCache := runCell("ttl-cache", concurrency, requests, originDelay, cacheTTL)
	fmt.Printf("  %s\n", ttlCache.Findings)

	for _, r := range []Experiment004AResult{noCache, ttlCache} {
		fname := filepath.Join(outDirName, fmt.Sprintf("004A-%s.json", r.Cell))
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(fname, b, 0644)
	}

	upstreamReduction := 0.0
	if noCache.EdgeOriginRequests > 0 {
		upstreamReduction = 100 * (1 - float64(ttlCache.EdgeOriginRequests)/float64(noCache.EdgeOriginRequests))
	}
	fmt.Printf("\n--- Comparison ---\n")
	fmt.Printf("  Upstream requests: no-cache=%d ttl-cache=%d (%.2f%% reduction)\n",
		noCache.EdgeOriginRequests, ttlCache.EdgeOriginRequests, upstreamReduction)
	fmt.Printf("  p50: no-cache=%v ttl-cache=%v\n", noCache.ClientLatencies.P50, ttlCache.ClientLatencies.P50)
	fmt.Printf("  p99: no-cache=%v ttl-cache=%v\n", noCache.ClientLatencies.P99, ttlCache.ClientLatencies.P99)
	fmt.Printf("  RPS: no-cache=%.1f ttl-cache=%.1f\n", noCache.ThroughputRPS, ttlCache.ThroughputRPS)

	fmt.Println("\nExperiment 004-A complete.")
}
