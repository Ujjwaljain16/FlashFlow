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

type Experiment004DResult struct {
	Experiment            string  `json:"experiment"`
	Timestamp             string  `json:"timestamp"`
	Cell                  string  `json:"cell"`
	Coalesce              bool    `json:"coalesce"`
	Concurrency           int     `json:"concurrency"`
	OriginDelayMs         int     `json:"origin_delay_ms"`
	CacheTTLMs            int     `json:"cache_ttl_ms"`
	SuccessfulRequests    int     `json:"successful_requests"`
	FailedRequests        int     `json:"failed_requests"`
	P50Ms                 float64 `json:"p50_ms"`
	P95Ms                 float64 `json:"p95_ms"`
	P99Ms                 float64 `json:"p99_ms"`
	UpstreamRequests      uint64  `json:"upstream_requests"`
	OriginPeakConcurrency int64   `json:"origin_peak_concurrency"`
	CoalesceLeads         uint64  `json:"coalesce_leads"`
	CoalesceShared        uint64  `json:"coalesce_shared"`
	Findings              string  `json:"findings"`
}

// runStampedeCell reproduces Experiment 004-C's stampede setup exactly
// (prime, confirm warm, force TTL past its expiry via MockClock, then a
// burst of `concurrency` concurrent requests for that same key) with one
// new knob: whether the edge coalesces concurrent misses. Everything else
// -- origin delay, TTL, workload shape -- is held fixed so the only
// variable between the two cells at a given concurrency is coalescing.
func runStampedeCell(cellName string, concurrency int, originDelay, cacheTTL time.Duration, coalesce bool) Experiment004DResult {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-004d", DefaultDelay: originDelay})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	mc := clock.NewMockClock(0)
	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-004d",
		OriginURL: origin.URL(),
		CacheTTL:  cacheTTL,
		Coalesce:  coalesce,
		Clock:     mc,
		TransportConfig: transport.TransportConfig{
			Label: "edge_origin_004d", MaxIdleConnsPerHost: 200, MaxIdleConns: 600,
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

	mc.Advance(cacheTTL + time.Millisecond) // guaranteed past TTL, same as 004-C's stampede cell

	baselineTransport := edge.TransportStats()

	res, err := httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL: edge.URL(), Path: "/data/hot", Requests: concurrency, Concurrency: concurrency,
	})
	if err != nil {
		log.Fatalf("burst benchmark failed: %v", err)
	}

	finalTransport := edge.TransportStats()
	upstreamRequests := finalTransport.RequestsCompleted - baselineTransport.RequestsCompleted
	peak := origin.ConcurrencyStats().Peak
	coalesceStats := edge.CoalesceStats()

	mode := "no coalescing (004-C behavior)"
	if coalesce {
		mode = "with coalescing"
	}
	finding := fmt.Sprintf(
		"%s, c=%d: %d/%d succeeded, p50=%.1fms p95=%.1fms p99=%.1fms. Upstream requests=%d, "+
			"Origin peak concurrency=%d, coalesce leads=%d shared=%d.",
		mode, concurrency, res.SuccessfulRequests, res.TotalRequests,
		msF(res.ClientLatencies.P50), msF(res.ClientLatencies.P95), msF(res.ClientLatencies.P99),
		upstreamRequests, peak, coalesceStats.Leads, coalesceStats.Shared,
	)

	return Experiment004DResult{
		Experiment: "004-D-request-coalescing", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Cell: cellName, Coalesce: coalesce, Concurrency: concurrency, OriginDelayMs: int(originDelay.Milliseconds()),
		CacheTTLMs:         int(cacheTTL.Milliseconds()),
		SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
		P50Ms: msF(res.ClientLatencies.P50), P95Ms: msF(res.ClientLatencies.P95), P99Ms: msF(res.ClientLatencies.P99),
		UpstreamRequests: upstreamRequests, OriginPeakConcurrency: peak,
		CoalesceLeads: coalesceStats.Leads, CoalesceShared: coalesceStats.Shared, Findings: finding,
	}
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-D: Request Coalescing")
	fmt.Println(" Exact 004-C stampede setup (hot key, forced TTL expiry via MockClock, concurrent burst),")
	fmt.Println(" run twice per concurrency level -- once as 004-C left it (no coalescing), once with the")
	fmt.Println(" edge's coalescer enabled -- to measure what coalescing actually buys against the same burst.")
	fmt.Println("==========================================================================================")

	const (
		originDelay = 100 * time.Millisecond // same as 004-C
		cacheTTL    = 50 * time.Millisecond
	)
	concurrencies := []int{10, 30, 100}

	var all []Experiment004DResult
	for _, c := range concurrencies {
		fmt.Printf("\n--- Concurrency %d ---\n", c)

		without := runStampedeCell(fmt.Sprintf("no-coalesce-c%03d", c), c, originDelay, cacheTTL, false)
		fmt.Printf("  %s\n", without.Findings)

		with := runStampedeCell(fmt.Sprintf("coalesce-c%03d", c), c, originDelay, cacheTTL, true)
		fmt.Printf("  %s\n", with.Findings)

		all = append(all, without, with)
	}

	for _, r := range all {
		fname := filepath.Join(outDirName, fmt.Sprintf("004D-%s.json", r.Cell))
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(fname, b, 0644)
	}

	fmt.Println("\n--- Comparison (no coalescing vs coalescing, by concurrency) ---")
	for i := 0; i < len(all); i += 2 {
		w, c := all[i], all[i+1]
		fmt.Printf("  c=%-3d  upstream: no-coalesce=%-3d coalesce=%-3d  peak-origin-concurrency: no-coalesce=%-3d coalesce=%-3d  p99: no-coalesce=%.1fms coalesce=%.1fms\n",
			w.Concurrency, w.UpstreamRequests, c.UpstreamRequests, w.OriginPeakConcurrency, c.OriginPeakConcurrency, w.P99Ms, c.P99Ms)
	}

	fmt.Println("\nExperiment 004-D complete.")
}
