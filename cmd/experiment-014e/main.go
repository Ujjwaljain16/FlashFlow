// Command experiment-014e is Stage 14 Track D: does an EXPLICIT real
// concurrency ceiling (Go's http.Transport.MaxConnsPerHost, now exposed
// via engine.RealExperimentConfig.MaxConnsPerHost) produce a
// qualitatively similar policy-regime transition to the virtual
// contention model, where Stage 13's UNCONSTRAINED real engine did not?
//
// Part 1 (mandatory per Stage 14 Section 19): a policy-neutral
// validation, entirely independent of RealEngine/proxy, that
// MaxConnsPerHost actually constrains observed concurrency before any
// policy comparison is trusted. Uses a raw http.Client + a hand-
// instrumented counting handler -- the simplest possible proof of the
// underlying Go mechanism.
//
// Part 2: RealEngine policy comparison (EWMA vs Adaptive) at three
// concurrency-ceiling levels on the same 15/30/60ms topology Stage 13's
// own experiment-011f/013k used, for direct comparability.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/014-scale-topology/results"

type ConcurrencyProbeResult struct {
	ConfiguredCeiling      int  `json:"configured_ceiling"`
	ObservedMaxConcurrency int  `json:"observed_max_concurrency"`
	ConstrainedCorrectly   bool `json:"constrained_correctly"`
}

// runConcurrencyProbe fires `requests` concurrent HTTP GETs at a slow
// handler through a client configured with the given MaxConnsPerHost,
// and directly measures the ACTUAL maximum number of requests the
// handler ever saw in flight simultaneously (via an atomic counter +
// CAS-based running max) -- ground truth, not inferred from timing.
func runConcurrencyProbe(ceiling, requests int) ConcurrencyProbeResult {
	var current int64
	var observedMax int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&current, 1)
		for {
			old := atomic.LoadInt64(&observedMax)
			if n <= old || atomic.CompareAndSwapInt64(&observedMax, old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt64(&current, -1)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxConnsPerHost: ceiling,
		},
	}
	var wg sync.WaitGroup
	wg.Add(requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}()
	}
	wg.Wait()

	observed := int(atomic.LoadInt64(&observedMax))
	return ConcurrencyProbeResult{
		ConfiguredCeiling:      ceiling,
		ObservedMaxConcurrency: observed,
		ConstrainedCorrectly:   ceiling <= 0 || observed <= ceiling,
	}
}

func runValidation() []ConcurrencyProbeResult {
	fmt.Println("\n=== Part 1: Policy-Neutral Concurrency Ceiling Validation ===")
	fmt.Println("(raw http.Client + counting handler, no RealEngine/proxy involved)")
	var results []ConcurrencyProbeResult
	for _, ceiling := range []int{1, 2, 4, 0} { // 0 = unlimited, the control case
		r := runConcurrencyProbe(ceiling, 20)
		results = append(results, r)
		label := fmt.Sprintf("%d", ceiling)
		if ceiling == 0 {
			label = "unlimited"
		}
		fmt.Printf("  ceiling=%-10s observed_max_concurrency=%-3d  constrained_correctly=%v\n", label, r.ObservedMaxConcurrency, r.ConstrainedCorrectly)
	}
	return results
}

// --- Part 2: RealEngine policy comparison under the validated ceiling ---

func edges() map[string]time.Duration {
	return map[string]time.Duration{
		"edge-a": 15 * time.Millisecond,
		"edge-b": 30 * time.Millisecond,
		"edge-c": 60 * time.Millisecond,
	}
}

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
}

type PolicyCell struct {
	Level    string  `json:"level"`
	Ceiling  int     `json:"max_conns_per_host"`
	Requests int     `json:"requests"`
	Policy   string  `json:"policy"`
	P50Ms    float64 `json:"p50_ms"`
	P99Ms    float64 `json:"p99_ms"`
	MaxShare float64 `json:"max_share"`
	Rejected int     `json:"errors_or_incomplete"`
}

func runPolicyComparison() []PolicyCell {
	fmt.Println("\n=== Part 2: RealEngine Policy Comparison Under a Validated Concurrency Ceiling ===")
	horizon := 4 * time.Second
	var cells []PolicyCell
	// ceiling=1 per edge, matching the VIRTUAL model's own Capacity=1
	// exactly (not an arbitrary choice): edge-a's ceiling-implied
	// throughput is 1 conn / 15ms ~= 66.7 req/s. EWMA concentrating ~97%
	// of traffic onto edge-a means offered load to edge-a must clearly
	// exceed 66.7 req/s to create genuine sustained queueing -- the
	// "above_ceiling" cell's total 100 req/s (400 requests / 4s) sends
	// ~97 req/s to edge-a specifically, comfortably over that ceiling-
	// implied throughput, unlike an earlier, under-powered ceiling=2 run
	// (133 req/s implied throughput, never actually exceeded) that
	// showed only a marginal, inconsistent effect -- corrected here
	// before trusting any conclusion from it.
	for i, level := range []struct {
		name     string
		requests int
		ceiling  int
	}{
		{"below_ceiling", 30, 1},
		{"near_ceiling", 75, 1},
		{"above_ceiling", 400, 1},
	} {
		fmt.Printf("\n-- %s (Requests=%d, MaxConnsPerHost=%d) --\n", level.name, level.requests, level.ceiling)
		seeds := replay.DeriveSeeds(int64(14400 + i))
		for _, ps := range []struct {
			name string
			spec replay.PolicySpec
		}{{"ewma", replay.EWMAPolicy()}, {"adaptive", replay.AdaptivePolicy()}} {
			exp := engine.Experiment{
				ID:       fmt.Sprintf("014e-%s-%s", level.name, ps.name),
				Scenario: replay.Scenario{Targets: targets(), Seeds: seeds},
				Policy:   ps.spec,
				Real: &engine.RealExperimentConfig{
					Edges:           edges(),
					TrafficPattern:  traffic.Constant,
					TrafficParams:   traffic.Params{Requests: level.requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)},
					MaxConnsPerHost: level.ceiling,
				},
			}
			r := engine.NewRealEngine()
			result, err := r.Run(exp)
			if err != nil {
				log.Fatalf("%s/%s: %v", level.name, ps.name, err)
			}
			p50, p99 := 0.0, 0.0
			if result.Real.Metrics.Histogram != nil {
				p50 = float64(result.Real.Metrics.Histogram.ValueAtPercentile(50)) / 1e6
				p99 = float64(result.Real.Metrics.Histogram.ValueAtPercentile(99)) / 1e6
			}
			total, maxCount := 0, 0
			for _, n := range result.Real.Metrics.RequestsTotal {
				total += int(n)
				if int(n) > maxCount {
					maxCount = int(n)
				}
			}
			maxShare := 0.0
			if total > 0 {
				maxShare = float64(maxCount) / float64(total)
			}
			cell := PolicyCell{Level: level.name, Ceiling: level.ceiling, Requests: level.requests, Policy: ps.name,
				P50Ms: p50, P99Ms: p99, MaxShare: maxShare, Rejected: level.requests - result.Real.Requests}
			cells = append(cells, cell)
			fmt.Printf("  %-10s p50=%7.2fms  p99=%9.2fms  max_share=%.3f  completed=%d/%d\n", ps.name, p50, p99, maxShare, result.Real.Requests, level.requests)
		}
	}
	return cells
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================")
	fmt.Println(" Experiment 014-E: RealEngine Concurrency Ceiling (Track D)")
	fmt.Println("=====================================================================")

	validation := runValidation()
	allValid := true
	for _, v := range validation {
		if !v.ConstrainedCorrectly {
			allValid = false
		}
	}
	if !allValid {
		log.Fatal("concurrency ceiling validation FAILED -- refusing to trust downstream policy comparisons built on a malfunctioning ceiling")
	}
	fmt.Println("\nCeiling mechanism validated: MaxConnsPerHost correctly bounds observed concurrency in every case tested.")

	cells := runPolicyComparison()

	fmt.Println("\n--- Does the real ceiling reproduce the virtual reversal's direction? ---")
	byLevel := map[string]map[string]PolicyCell{}
	for _, c := range cells {
		if byLevel[c.Level] == nil {
			byLevel[c.Level] = map[string]PolicyCell{}
		}
		byLevel[c.Level][c.Policy] = c
	}
	for _, level := range []string{"below_ceiling", "near_ceiling", "above_ceiling"} {
		e, a := byLevel[level]["ewma"], byLevel[level]["adaptive"]
		winner := "EWMA"
		if a.P99Ms < e.P99Ms {
			winner = "Adaptive"
		}
		fmt.Printf("%-14s ewma_p99=%9.2fms  adaptive_p99=%9.2fms  ewma_max_share=%.3f  adaptive_max_share=%.3f  -- %s\n",
			level, e.P99Ms, a.P99Ms, e.MaxShare, a.MaxShare, winner)
	}

	out := struct {
		Experiment string                   `json:"experiment"`
		Timestamp  string                   `json:"timestamp"`
		Validation []ConcurrencyProbeResult `json:"validation"`
		Cells      []PolicyCell             `json:"cells"`
	}{Experiment: "014-E-real-concurrency-ceiling", Timestamp: time.Now().UTC().Format(time.RFC3339), Validation: validation, Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014E-real-concurrency-ceiling.json"), b, 0644)
	fmt.Println("\nExperiment 014-E complete.")
}
