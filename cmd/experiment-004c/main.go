package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/httpx"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

const outDirName = "experiments/004-caching-failures/results"

type Experiment004CResult struct {
	Experiment              string  `json:"experiment"`
	Timestamp               string  `json:"timestamp"`
	Cell                    string  `json:"cell"`
	Concurrency             int     `json:"concurrency"`
	OriginDelayMs           int     `json:"origin_delay_ms"`
	CacheTTLMs              int     `json:"cache_ttl_ms"`
	EntryExpiredBeforeBurst bool    `json:"entry_expired_before_burst"`
	SuccessfulRequests      int     `json:"successful_requests"`
	FailedRequests          int     `json:"failed_requests"`
	P50Ms                   float64 `json:"p50_ms"`
	P95Ms                   float64 `json:"p95_ms"`
	P99Ms                   float64 `json:"p99_ms"`
	UpstreamRequests        uint64  `json:"upstream_requests"`
	DuplicateFetches        int64   `json:"duplicate_fetches"`
	OriginPeakConcurrency   int64   `json:"origin_peak_concurrency"`
	CacheMisses             uint64  `json:"cache_misses"`
	Findings                string  `json:"findings"`
}

// runBurstCell primes a single hot key, optionally forces its TTL to
// elapse (via an explicit, deterministic clock.MockClock — not a real
// sleep), then fires `concurrency` requests for that exact key at
// concurrency `concurrency` (one request per worker, so all of them are
// dispatched essentially as fast as Go's scheduler allows, maximizing the
// "arrived at the same instant" property a stampede needs).
func runBurstCell(cellName string, concurrency int, originDelay, cacheTTL time.Duration, expireBeforeBurst bool) Experiment004CResult {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-004c", DefaultDelay: originDelay})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	mc := clock.NewMockClock(0)
	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-004c",
		OriginURL: origin.URL(),
		CacheTTL:  cacheTTL,
		Clock:     mc,
		TransportConfig: transport.TransportConfig{
			Label: "edge_origin_004c", MaxIdleConnsPerHost: 200, MaxIdleConns: 600,
		},
	})
	if err != nil {
		log.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		log.Fatalf("failed to start edge: %v", err)
	}
	defer edge.Stop(context.Background())

	client := &http.Client{Timeout: 5 * time.Second}
	prime := func() string {
		resp, err := client.Get(edge.URL() + "/data/hot")
		if err != nil {
			log.Fatalf("priming request failed: %v", err)
		}
		defer resp.Body.Close()
		return resp.Header.Get(httpx.HeaderCacheStatus)
	}

	if status := prime(); status != "MISS" {
		log.Fatalf("expected priming request to be a MISS (cold cache), got %q", status)
	}
	if status := prime(); status != "HIT" {
		log.Fatalf("expected second request to confirm the entry is warm (HIT), got %q", status)
	}

	if expireBeforeBurst {
		mc.Advance(cacheTTL + time.Millisecond) // guaranteed past TTL
	}

	baselineTransport := edge.TransportStats()
	baselineCache := edge.CacheStats()

	res, err := httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL: edge.URL(), Path: "/data/hot", Requests: concurrency, Concurrency: concurrency,
	})
	if err != nil {
		log.Fatalf("burst benchmark failed: %v", err)
	}

	finalTransport := edge.TransportStats()
	finalCache := edge.CacheStats()
	upstreamRequests := finalTransport.RequestsCompleted - baselineTransport.RequestsCompleted
	misses := finalCache.Misses - baselineCache.Misses
	peak := origin.ConcurrencyStats().Peak

	duplicateFetches := int64(upstreamRequests) - 1
	if duplicateFetches < 0 {
		duplicateFetches = 0
	}

	scenario := "control (cache stays warm)"
	if expireBeforeBurst {
		scenario = "stampede (cache expired before the burst)"
	}
	finding := fmt.Sprintf(
		"%s, c=%d: %d/%d succeeded, p50=%.1fms p95=%.1fms p99=%.1fms. Upstream requests=%d (cache misses=%d), "+
			"duplicate fetches=%d, Origin peak concurrency=%d.",
		scenario, concurrency, res.SuccessfulRequests, res.TotalRequests,
		msF(res.ClientLatencies.P50), msF(res.ClientLatencies.P95), msF(res.ClientLatencies.P99),
		upstreamRequests, misses, duplicateFetches, peak,
	)

	return Experiment004CResult{
		Experiment: "004-C-cache-stampede", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Cell: cellName, Concurrency: concurrency, OriginDelayMs: int(originDelay.Milliseconds()),
		CacheTTLMs: int(cacheTTL.Milliseconds()), EntryExpiredBeforeBurst: expireBeforeBurst,
		SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
		P50Ms: msF(res.ClientLatencies.P50), P95Ms: msF(res.ClientLatencies.P95), P99Ms: msF(res.ClientLatencies.P99),
		UpstreamRequests: upstreamRequests, DuplicateFetches: duplicateFetches,
		OriginPeakConcurrency: peak, CacheMisses: misses, Findings: finding,
	}
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-C: Cache Stampede")
	fmt.Println(" Single hot key, primed and confirmed warm, TTL forced to elapse (via MockClock), then a")
	fmt.Println(" concurrent burst for that exact key -- with a same-burst, non-expired control for comparison.")
	fmt.Println("==========================================================================================")

	const (
		originDelay = 100 * time.Millisecond // wide stampede window, deliberately
		cacheTTL    = 50 * time.Millisecond
	)
	concurrencies := []int{10, 30, 100}

	var all []Experiment004CResult
	for _, c := range concurrencies {
		fmt.Printf("\n--- Concurrency %d ---\n", c)

		control := runBurstCell(fmt.Sprintf("control-c%03d", c), c, originDelay, cacheTTL, false)
		fmt.Printf("  %s\n", control.Findings)

		stampede := runBurstCell(fmt.Sprintf("stampede-c%03d", c), c, originDelay, cacheTTL, true)
		fmt.Printf("  %s\n", stampede.Findings)

		all = append(all, control, stampede)
	}

	for _, r := range all {
		fname := filepath.Join(outDirName, fmt.Sprintf("004C-%s.json", r.Cell))
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(fname, b, 0644)
	}

	fmt.Println("\n--- Comparison (control vs stampede, by concurrency) ---")
	for i := 0; i < len(all); i += 2 {
		c, s := all[i], all[i+1]
		fmt.Printf("  c=%-3d  upstream: control=%-3d stampede=%-3d  peak-origin-concurrency: control=%-3d stampede=%-3d  p99: control=%.1fms stampede=%.1fms\n",
			c.Concurrency, c.UpstreamRequests, s.UpstreamRequests, c.OriginPeakConcurrency, s.OriginPeakConcurrency, c.P99Ms, s.P99Ms)
	}

	fmt.Println("\nExperiment 004-C complete.")
}
