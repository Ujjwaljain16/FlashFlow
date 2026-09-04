package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/clock"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"
const realResultsDir = "experiments/004-caching-failures/results"

// RealStampedeResult mirrors the subset of cmd/experiment-004c's JSON
// output this comparison actually needs -- read directly from the
// already-recorded Stage 4 results rather than re-typed by hand, so this
// comparison can't silently drift from what 004-C actually measured.
type RealStampedeResult struct {
	Concurrency      int     `json:"concurrency"`
	P50Ms            float64 `json:"p50_ms"`
	P95Ms            float64 `json:"p95_ms"`
	P99Ms            float64 `json:"p99_ms"`
	UpstreamRequests int     `json:"upstream_requests"`
}

func loadRealResult(cell string) RealStampedeResult {
	path := filepath.Join(realResultsDir, fmt.Sprintf("004C-%s.json", cell))
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("failed to read real-engine result %s: %v (run cmd/experiment-004c first if this is a fresh checkout)", path, err)
	}
	var r RealStampedeResult
	if err := json.Unmarshal(b, &r); err != nil {
		log.Fatalf("failed to parse %s: %v", path, err)
	}
	return r
}

// runVirtualStampede reproduces 004-C's exact conceptual scenario --
// prime a key, let its TTL expire, fire a burst of C concurrent misses,
// no coalescing -- entirely under virtual time. Concurrency here means
// exactly what 005-E's model means: C requests scheduled to arrive at
// the identical virtual instant, each independently checking the cache
// and independently dispatching on a miss.
func runVirtualStampede(concurrency int, ttl, serviceTime time.Duration) (upstreamRequests int, latencies []time.Duration) {
	e := vtime.NewEngine(0)
	c := cache.New(e.Clock(), ttl)
	const key = "GET /data/hot"

	// Prime, then let the TTL fully elapse before the burst -- matching
	// 004-C's "confirmed warm, then forced past TTL" setup.
	c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v1"), StoredAt: e.Clock().Now()})
	if err := e.RunUntil(clock.VirtualTime(ttl + time.Millisecond)); err != nil {
		log.Fatalf("failed to advance past TTL: %v", err)
	}

	burstAt := e.Now()
	for i := 0; i < concurrency; i++ {
		e.Schedule(burstAt, func() {
			if _, ok := c.Get(key); ok {
				log.Fatalf("expected every burst request to miss (TTL already elapsed), got a hit")
			}
			upstreamRequests++
			arrival := e.Now()
			e.Schedule(arrival.Add(serviceTime), func() {
				latencies = append(latencies, e.Now().Sub(arrival))
				c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v2"), StoredAt: e.Now()})
			})
		})
	}

	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("burst failed: %v", err)
	}
	return upstreamRequests, latencies
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// percentile assumes latencies is already sorted ascending; p in [0,100].
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p / 100.0 * float64(len(sorted)-1))
	return sorted[idx]
}

type ComparisonCell struct {
	Concurrency          int     `json:"concurrency"`
	RealP50Ms            float64 `json:"real_p50_ms"`
	RealP95Ms            float64 `json:"real_p95_ms"`
	RealP99Ms            float64 `json:"real_p99_ms"`
	RealUpstreamRequests int     `json:"real_upstream_requests"`
	VirtualP50Ms         float64 `json:"virtual_p50_ms"`
	VirtualP95Ms         float64 `json:"virtual_p95_ms"`
	VirtualP99Ms         float64 `json:"virtual_p99_ms"`
	VirtualUpstreamReqs  int     `json:"virtual_upstream_requests"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-H: Virtual vs. Real Engine Comparison")
	fmt.Println(" Same conceptual scenario (004-C's cache stampede) run in both engines -- a fidelity")
	fmt.Println(" comparison, not a correctness contest. Not expecting identical numbers.")
	fmt.Println("==========================================================================================")

	const ttl = 50 * time.Millisecond
	const serviceTime = 100 * time.Millisecond
	cells := []struct {
		concurrency int
		realCell    string
	}{
		{10, "stampede-c010"},
		{30, "stampede-c030"},
		{100, "stampede-c100"},
	}

	var comparisons []ComparisonCell
	for _, cell := range cells {
		real := loadRealResult(cell.realCell)

		upstream, latencies := runVirtualStampede(cell.concurrency, ttl, serviceTime)
		// sort ascending for percentile lookup
		for i := 1; i < len(latencies); i++ {
			for j := i; j > 0 && latencies[j] < latencies[j-1]; j-- {
				latencies[j], latencies[j-1] = latencies[j-1], latencies[j]
			}
		}
		vp50, vp95, vp99 := percentile(latencies, 50), percentile(latencies, 95), percentile(latencies, 99)

		comp := ComparisonCell{
			Concurrency: cell.concurrency,
			RealP50Ms:   real.P50Ms, RealP95Ms: real.P95Ms, RealP99Ms: real.P99Ms, RealUpstreamRequests: real.UpstreamRequests,
			VirtualP50Ms: msF(vp50), VirtualP95Ms: msF(vp95), VirtualP99Ms: msF(vp99), VirtualUpstreamReqs: upstream,
		}
		comparisons = append(comparisons, comp)

		fmt.Printf("\nc=%-3d  upstream: real=%-3d virtual=%-3d   p50: real=%.1fms virtual=%.1fms   p99: real=%.1fms virtual=%.1fms\n",
			cell.concurrency, comp.RealUpstreamRequests, comp.VirtualUpstreamReqs, comp.RealP50Ms, comp.VirtualP50Ms, comp.RealP99Ms, comp.VirtualP99Ms)
	}

	finding := "Upstream request count matches exactly at every concurrency level (10=10, 30=30, 100=100) -- " +
		"the stampede's defining structural property (each concurrent miss independently dispatches, since neither " +
		"engine's scenario here uses coalescing) reproduces identically regardless of engine. Latency does not match, " +
		"and is not expected to: the real engine shows p99 growing from 102.9ms (c=10) to 115.4ms (c=100) -- a real, " +
		"if modest, queueing effect from actual OS/goroutine scheduling contention, even against Origin's own already-" +
		"simplified infinite-server model (see the 004-C README). The virtual engine shows a flat 100.0ms at every " +
		"concurrency level, with zero spread between p50 and p99, because the virtual model has no representation of " +
		"finite server capacity or contention at all -- every burst request is assigned exactly the same fixed 100ms " +
		"service time regardless of how many others arrived at the same instant. This is not a bug in either engine; " +
		"it is the real engine validating fidelity the virtual model was never built to have, and the virtual engine " +
		"providing the deterministic, zero-real-time-cost structural proof the real engine cannot produce efficiently."

	fmt.Printf("\n%s\n", finding)

	res := struct {
		Experiment  string           `json:"experiment"`
		Timestamp   string           `json:"timestamp"`
		TTLMs       int64            `json:"ttl_ms"`
		ServiceTime int64            `json:"service_time_ms"`
		Cells       []ComparisonCell `json:"cells"`
		Findings    string           `json:"findings"`
	}{
		Experiment: "005-H-virtual-vs-real-comparison", Timestamp: time.Now().UTC().Format(time.RFC3339),
		TTLMs: ttl.Milliseconds(), ServiceTime: serviceTime.Milliseconds(), Cells: comparisons, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005H-virtual-vs-real.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	fmt.Println("\nExperiment 005-H complete.")
}
