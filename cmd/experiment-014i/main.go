// Command experiment-014i is Stage 14 Section 25: statistical
// confirmation, via 10 independent seeds, of the two most important
// Stage 14 claims made so far at a single seed each:
//
//  1. experiment-014c's central finding that Adaptive beats EWMA at the
//     N=8 near-boundary point.
//  2. experiment-014f's falsifier that EWMA loses to round-robin at that
//     SAME point -- the result that broke the "load-aware always beats
//     load-blind" framing.
//
// Same N=8 graduated topology, Capacity=1, Requests=381 used throughout
// 014c/014d/014f/014g/014h. Root seeds are a fresh, disjoint block
// (14700-14709) from every seed used in those experiments, per Section
// 31's own warning against accidental seed coupling.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/014-scale-topology/results"
const horizon = 4 * time.Second
const capacity = 1
const targetCount = 8
const requests = 381
const seedCount = 10

func targets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, targetCount)
	for i := 0; i < targetCount; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: capacity}
	}
	return out
}

func meanLatency(wr *replay.WorldResult) float64 {
	if len(wr.Completions) == 0 {
		return 0
	}
	ms := make([]float64, len(wr.Completions))
	for i, c := range wr.Completions {
		ms[i] = float64(c.Latency.Microseconds()) / 1000.0
	}
	m, _ := statistics.Mean(ms)
	return m
}

type SeedResult struct {
	Seed        int64   `json:"seed"`
	RRMeanMs    float64 `json:"rr_mean_ms"`
	EWMAMeanMs  float64 `json:"ewma_mean_ms"`
	AdaptMeanMs float64 `json:"adaptive_mean_ms"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 014-I: Statistical Confirmation at the N=8 Near-Boundary Point (Section 25)")
	fmt.Println("=====================================================================================")

	var seedResults []SeedResult
	var rrMeans, ewmaMeans, adaptiveMeans []float64
	for i := 0; i < seedCount; i++ {
		rootSeed := int64(14700 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5), JitterFraction: 0.3,
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("seed %d: generating traffic: %v", rootSeed, err)
		}
		scenario := replay.Scenario{Targets: targets(), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("014i-seed%d", rootSeed), Scenario: scenario, Policy: replay.RoundRobinPolicy()}

		rRR, err := v.Run(exp)
		if err != nil {
			log.Fatalf("seed %d rr: %v", rootSeed, err)
		}
		rEwma, err := v.Replay(exp, replay.EWMAPolicy())
		if err != nil {
			log.Fatalf("seed %d ewma: %v", rootSeed, err)
		}
		rAdaptive, err := v.Replay(exp, replay.AdaptivePolicy())
		if err != nil {
			log.Fatalf("seed %d adaptive: %v", rootSeed, err)
		}
		rr, ewma, adaptive := meanLatency(rRR.WorldResult), meanLatency(rEwma.WorldResult), meanLatency(rAdaptive.WorldResult)
		rrMeans = append(rrMeans, rr)
		ewmaMeans = append(ewmaMeans, ewma)
		adaptiveMeans = append(adaptiveMeans, adaptive)
		seedResults = append(seedResults, SeedResult{Seed: rootSeed, RRMeanMs: rr, EWMAMeanMs: ewma, AdaptMeanMs: adaptive})
		fmt.Printf("  seed=%d  rr=%9.2fms  ewma=%9.2fms  adaptive=%9.2fms\n", rootSeed, rr, ewma, adaptive)
	}

	fmt.Println("\n--- Claim 1: Adaptive beats EWMA ---")
	adaptiveWins := 0
	for i := range ewmaMeans {
		if adaptiveMeans[i] < ewmaMeans[i] {
			adaptiveWins++
		}
	}
	delta1, err := statistics.CliffsDelta(ewmaMeans, adaptiveMeans)
	if err != nil {
		log.Fatalf("CliffsDelta (ewma vs adaptive): %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	ci1, err := statistics.BootstrapDiffCI(ewmaMeans, adaptiveMeans, statistics.MeanStat, 0.95, 10000, rng)
	if err != nil {
		log.Fatalf("BootstrapDiffCI (ewma vs adaptive): %v", err)
	}
	fmt.Printf("Adaptive faster in %d/%d seeds. Cliff's Delta: %.3f (%s). Bootstrap 95%% CI on (ewma-adaptive): [%.2f, %.2f]ms.\n",
		adaptiveWins, seedCount, delta1.Delta, delta1.Magnitude, ci1.Lower, ci1.Upper)

	fmt.Println("\n--- Claim 2 (the falsifier): round-robin beats EWMA ---")
	rrWins := 0
	for i := range ewmaMeans {
		if rrMeans[i] < ewmaMeans[i] {
			rrWins++
		}
	}
	delta2, err := statistics.CliffsDelta(ewmaMeans, rrMeans)
	if err != nil {
		log.Fatalf("CliffsDelta (ewma vs rr): %v", err)
	}
	rng2 := rand.New(rand.NewSource(43))
	ci2, err := statistics.BootstrapDiffCI(ewmaMeans, rrMeans, statistics.MeanStat, 0.95, 10000, rng2)
	if err != nil {
		log.Fatalf("BootstrapDiffCI (ewma vs rr): %v", err)
	}
	fmt.Printf("Round-robin faster than EWMA in %d/%d seeds. Cliff's Delta: %.3f (%s). Bootstrap 95%% CI on (ewma-rr): [%.2f, %.2f]ms.\n",
		rrWins, seedCount, delta2.Delta, delta2.Magnitude, ci2.Lower, ci2.Upper)

	if rrWins == seedCount {
		fmt.Println("\nThe falsifier is NOT a single-seed artifact: round-robin beats EWMA in EVERY independent seed tested.")
	} else if rrWins == 0 {
		fmt.Println("\nThe single-seed falsifier does NOT replicate: EWMA beat round-robin in every seed here -- the")
		fmt.Println("original 014f result may have been a seed-specific artifact and should be treated cautiously.")
	} else {
		fmt.Printf("\nThe falsifier replicates PARTIALLY: round-robin beat EWMA in %d/%d seeds, not all -- report as a\n", rrWins, seedCount)
		fmt.Println("probabilistic, not universal, effect at this operating point.")
	}

	// AdaptiveVsEWMACI*/RRVsEWMACI* close a real gap an independent audit
	// found: this experiment already computed both bootstrap 95% CIs
	// (ci1, ci2 above) -- exactly the numbers Stage14.md's own "Statistical
	// Robustness" section cites as headline results -- and printed them to
	// stdout, but never persisted them here, leaving them unverifiable
	// from the committed artifact alone (recomputing from the still-
	// present seed_results confirms them, but a reader shouldn't have to).
	out := struct {
		Experiment            string       `json:"experiment"`
		Timestamp             string       `json:"timestamp"`
		SeedResults           []SeedResult `json:"seed_results"`
		AdaptiveVsEWMADelta   float64      `json:"adaptive_vs_ewma_cliffs_delta"`
		AdaptiveVsEWMAWins    int          `json:"adaptive_vs_ewma_wins"`
		AdaptiveVsEWMACILower float64      `json:"adaptive_vs_ewma_ci_lower_ms"`
		AdaptiveVsEWMACIUpper float64      `json:"adaptive_vs_ewma_ci_upper_ms"`
		RRVsEWMADelta         float64      `json:"rr_vs_ewma_cliffs_delta"`
		RRVsEWMAWins          int          `json:"rr_vs_ewma_wins"`
		RRVsEWMACILower       float64      `json:"rr_vs_ewma_ci_lower_ms"`
		RRVsEWMACIUpper       float64      `json:"rr_vs_ewma_ci_upper_ms"`
	}{
		Experiment: "014-I-statistical-confirmation-n8-boundary", Timestamp: time.Now().UTC().Format(time.RFC3339),
		SeedResults: seedResults, AdaptiveVsEWMADelta: delta1.Delta, AdaptiveVsEWMAWins: adaptiveWins,
		AdaptiveVsEWMACILower: ci1.Lower, AdaptiveVsEWMACIUpper: ci1.Upper,
		RRVsEWMADelta: delta2.Delta, RRVsEWMAWins: rrWins,
		RRVsEWMACILower: ci2.Lower, RRVsEWMACIUpper: ci2.Upper,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014I-statistical-confirmation.json"), b, 0644)
	fmt.Println("\nExperiment 014-I complete.")
}
