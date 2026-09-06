// Command experiment-011h strengthens Stage 11's single most load-bearing
// claim (Program A/Section 7: EWMA beats Adaptive on mean latency under
// severe heterogeneity) with actual statistical discipline rather than a
// single-seed point estimate. Every Program A-F number so far comes from
// ONE seed per configuration -- informative for finding regime structure,
// but exactly what this stage's own brief warns against ("no single-run
// strong claims"). This program reruns the severe/constant/none scenario
// across 12 independent traffic seeds (jitter enabled so the seed
// actually matters -- Program A's own constant pattern has zero jitter by
// default, so varying its seed alone would silently produce byte-
// identical arrivals) and applies internal/statistics's own Cliff's
// Delta and bootstrap CI tools, chosen because the question here is "is
// EWMA's advantage a real, direction-consistent effect or could it be
// noise" -- a location-difference-with-effect-size question, not a
// significance-test-by-habit one.
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

const outDirName = "experiments/011-research-validation/results"

const nSeeds = 12

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================")
	fmt.Println(" Experiment 011-H: Statistical Robustness of Program A's Flagship Claim")
	fmt.Println(" (EWMA vs Adaptive mean latency, severe heterogeneity, 12 traffic seeds)")
	fmt.Println("=====================================================================")

	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
	horizon := 4 * time.Second

	var ewmaMeans, adaptiveMeans []float64
	for i := 0; i < nSeeds; i++ {
		rootSeed := int64(9000 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
			JitterFraction: 0.3, // enables genuine seed-to-seed variation; Program A's own 0-jitter default would make every seed produce byte-identical arrivals
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("seed %d: generating traffic: %v", rootSeed, err)
		}
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("011h-seed%d", rootSeed), Scenario: scenario, Policy: replay.EWMAPolicy()}

		rEwma, err := v.Run(exp)
		if err != nil {
			log.Fatalf("seed %d ewma: %v", rootSeed, err)
		}
		rAdaptive, err := v.Replay(exp, replay.AdaptivePolicy())
		if err != nil {
			log.Fatalf("seed %d adaptive: %v", rootSeed, err)
		}
		ewmaMeans = append(ewmaMeans, meanLatency(rEwma.WorldResult))
		adaptiveMeans = append(adaptiveMeans, meanLatency(rAdaptive.WorldResult))
	}

	fmt.Println("\nseed#  ewma mean(ms)  adaptive mean(ms)  ewma faster?")
	for i := range ewmaMeans {
		fmt.Printf("%-6d %13.2f %19.2f  %v\n", i, ewmaMeans[i], adaptiveMeans[i], ewmaMeans[i] < adaptiveMeans[i])
	}

	delta, err := statistics.CliffsDelta(adaptiveMeans, ewmaMeans) // positive delta means adaptive tends to have HIGHER (worse) latency than ewma
	if err != nil {
		log.Fatalf("CliffsDelta: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	diffCI, err := statistics.BootstrapDiffCI(adaptiveMeans, ewmaMeans, statistics.MeanStat, 0.95, 10000, rng)
	if err != nil {
		log.Fatalf("BootstrapDiffCI: %v", err)
	}

	fmt.Printf("\nCliff's Delta (adaptive vs ewma mean latency): delta=%.3f (%s) -- positive means adaptive tends higher (worse)\n", delta.Delta, delta.Magnitude)
	fmt.Printf("Bootstrap 95%% CI on (adaptive mean - ewma mean): estimate=%.2fms  [%.2f, %.2f]ms  (n_resamples=%d)\n",
		diffCI.Estimate, diffCI.Lower, diffCI.Upper, diffCI.NResamples)
	if diffCI.Lower > 0 {
		fmt.Println("CI excludes zero and is entirely positive: EWMA's advantage over Adaptive is a robust, direction-consistent effect across seeds, not single-run noise.")
	} else {
		fmt.Println("CI includes zero: the direction of EWMA's advantage is NOT reliably distinguishable from noise across these seeds.")
	}

	out := struct {
		Experiment      string                       `json:"experiment"`
		Timestamp       string                       `json:"timestamp"`
		EwmaMeansMs     []float64                    `json:"ewma_means_ms"`
		AdaptiveMeansMs []float64                    `json:"adaptive_means_ms"`
		CliffsDelta     statistics.CliffsDeltaResult `json:"cliffs_delta_adaptive_vs_ewma"`
		BootstrapDiff   statistics.BootstrapResult   `json:"bootstrap_diff_ci_adaptive_minus_ewma_ms"`
	}{
		Experiment: "011-H-statistical-robustness", Timestamp: time.Now().UTC().Format(time.RFC3339),
		EwmaMeansMs: ewmaMeans, AdaptiveMeansMs: adaptiveMeans, CliffsDelta: delta, BootstrapDiff: diffCI,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011H-statistical-robustness.json"), b, 0644)
	fmt.Println("\nExperiment 011-H complete.")
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
