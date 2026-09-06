// Command experiment-012e strengthens Stage 12's own single most
// load-bearing claim (experiment-012a: Adaptive reverses EWMA's flat-
// model win once Capacity=1 makes concentration carry a real queueing
// cost) with the same statistical discipline experiment-011h applied to
// the original flat-model finding: replication across independent
// traffic seeds, an effect size, and a bootstrap CI -- not a single-seed
// point estimate.
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

const outDirName = "experiments/012-model-fidelity/results"

const nSeeds = 12

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: 1},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: 1},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: 1},
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===================================================================================")
	fmt.Println(" Experiment 012-E: Statistical Robustness of the Contention-Reversal Finding")
	fmt.Println(" (Adaptive vs EWMA mean latency, Capacity=1, severe heterogeneity, 12 traffic seeds)")
	fmt.Println("===================================================================================")

	horizon := 4 * time.Second
	var ewmaMeans, adaptiveMeans []float64
	for i := 0; i < nSeeds; i++ {
		rootSeed := int64(9500 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
			JitterFraction: 0.3,
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("seed %d: generating traffic: %v", rootSeed, err)
		}
		scenario := replay.Scenario{Targets: targets(), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("012e-seed%d", rootSeed), Scenario: scenario, Policy: replay.EWMAPolicy()}

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

	fmt.Println("\nseed#  ewma mean(ms)  adaptive mean(ms)  adaptive faster?")
	for i := range ewmaMeans {
		fmt.Printf("%-6d %13.2f %19.2f  %v\n", i, ewmaMeans[i], adaptiveMeans[i], adaptiveMeans[i] < ewmaMeans[i])
	}

	delta, err := statistics.CliffsDelta(ewmaMeans, adaptiveMeans) // positive delta means ewma tends higher (worse) than adaptive
	if err != nil {
		log.Fatalf("CliffsDelta: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	diffCI, err := statistics.BootstrapDiffCI(ewmaMeans, adaptiveMeans, statistics.MeanStat, 0.95, 10000, rng)
	if err != nil {
		log.Fatalf("BootstrapDiffCI: %v", err)
	}

	fmt.Printf("\nCliff's Delta (ewma vs adaptive mean latency): delta=%.3f (%s) -- positive means ewma tends higher (worse) under contention\n", delta.Delta, delta.Magnitude)
	fmt.Printf("Bootstrap 95%% CI on (ewma mean - adaptive mean): estimate=%.2fms  [%.2f, %.2f]ms  (n_resamples=%d)\n",
		diffCI.Estimate, diffCI.Lower, diffCI.Upper, diffCI.NResamples)
	if diffCI.Lower > 0 {
		fmt.Println("CI excludes zero and is entirely positive: Adaptive's advantage over EWMA under contention is a robust, direction-consistent effect across seeds, not single-run noise.")
	} else {
		fmt.Println("CI includes zero: the direction of the reversal is NOT reliably distinguishable from noise across these seeds.")
	}

	out := struct {
		Experiment      string                       `json:"experiment"`
		Timestamp       string                       `json:"timestamp"`
		EwmaMeansMs     []float64                    `json:"ewma_means_ms"`
		AdaptiveMeansMs []float64                    `json:"adaptive_means_ms"`
		CliffsDelta     statistics.CliffsDeltaResult `json:"cliffs_delta_ewma_vs_adaptive"`
		BootstrapDiff   statistics.BootstrapResult   `json:"bootstrap_diff_ci_ewma_minus_adaptive_ms"`
	}{
		Experiment: "012-E-contention-reversal-robustness", Timestamp: time.Now().UTC().Format(time.RFC3339),
		EwmaMeansMs: ewmaMeans, AdaptiveMeansMs: adaptiveMeans, CliffsDelta: delta, BootstrapDiff: diffCI,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "012E-contention-reversal-robustness.json"), b, 0644)
	fmt.Println("\nExperiment 012-E complete.")
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
