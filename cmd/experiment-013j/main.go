// Command experiment-013j covers two remaining Stage 13 requirements:
//
// Part 1 (statistical confirmation, Section 18): the flagship
// Capacity=1 reversal was already confirmed robust across 12 seeds in
// Stage 12 (experiment-012e). This adds targeted confirmation AT THE
// BOUNDARY specifically -- experiment-013c found the transition sits
// around offered rho 0.89 (150 req/s at Capacity=2) -- confirming the
// WINNER actually flips consistently across independent seeds drawn
// from that specific near-boundary rate, not just deep inside the
// already-obviously-collapsed Capacity=1 regime.
//
// Part 2 (Section 23, policy configuration sensitivity): repeats the
// flagship Capacity=1 comparison with Stage 8's ACTUAL tuned
// AdaptiveConfig instead of the hand-chosen default, to determine
// whether the discovered regime is structural to Adaptive or specific
// to one parameterization.
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
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/013-regime-discovery/results"

func targets(capacity int) []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
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

func runNearBoundaryConfirmation() {
	fmt.Println("\n=== Part 1: Near-Boundary Statistical Confirmation ===")
	fmt.Println("(Capacity=2, Requests=600 -- the near-boundary point experiment-013c found at offered_rho~0.89,")
	fmt.Println(" not deep inside the already-obvious Capacity=1 collapse)")
	const horizon = 4 * time.Second
	const capacity = 2
	const requestCount = 600

	var ewmaMeans, adaptiveMeans []float64
	for i := 0; i < 10; i++ {
		rootSeed := int64(13900 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requestCount, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5), JitterFraction: 0.3,
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("seed %d: generating traffic: %v", rootSeed, err)
		}
		scenario := replay.Scenario{Targets: targets(capacity), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013j-seed%d", rootSeed), Scenario: scenario, Policy: replay.EWMAPolicy()}

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
	adaptiveWins := 0
	for i := range ewmaMeans {
		faster := adaptiveMeans[i] < ewmaMeans[i]
		if faster {
			adaptiveWins++
		}
		fmt.Printf("%-6d %13.2f %19.2f  %v\n", i, ewmaMeans[i], adaptiveMeans[i], faster)
	}
	delta, err := statistics.CliffsDelta(ewmaMeans, adaptiveMeans)
	if err != nil {
		log.Fatalf("CliffsDelta: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	diffCI, err := statistics.BootstrapDiffCI(ewmaMeans, adaptiveMeans, statistics.MeanStat, 0.95, 10000, rng)
	if err != nil {
		log.Fatalf("BootstrapDiffCI: %v", err)
	}
	fmt.Printf("\nAdaptive faster in %d/10 seeds at this near-boundary point.\n", adaptiveWins)
	fmt.Printf("Cliff's Delta: %.3f (%s). Bootstrap 95%% CI on (ewma-adaptive): [%.2f, %.2f]ms.\n", delta.Delta, delta.Magnitude, diffCI.Lower, diffCI.Upper)
}

func runDefaultVsTuned() {
	fmt.Println("\n=== Part 2: Default vs Stage-8-Tuned Adaptive at the Flagship Boundary ===")
	const horizon = 4 * time.Second
	const capacity = 1
	seeds := replay.DeriveSeeds(1001)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}
	scenario := replay.Scenario{Targets: targets(capacity), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "013j-default-vs-tuned", Scenario: scenario, Policy: replay.EWMAPolicy()}

	rEwma, err := v.Run(exp)
	if err != nil {
		log.Fatalf("ewma: %v", err)
	}
	rDefault, err := v.Replay(exp, replay.AdaptivePolicy())
	if err != nil {
		log.Fatalf("adaptive-default: %v", err)
	}
	tunedCfg := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: 0.161, Latency: 0.568, Cache: 0.051, Cost: 0.220},
		ReferenceLatency: 192 * time.Millisecond, StaleAfter: 3740 * time.Millisecond,
	}
	rTuned, err := v.Replay(exp, replay.AdaptivePolicyWithConfig(tunedCfg))
	if err != nil {
		log.Fatalf("adaptive-tuned: %v", err)
	}

	ewmaMean, defaultMean, tunedMean := meanLatency(rEwma.WorldResult), meanLatency(rDefault.WorldResult), meanLatency(rTuned.WorldResult)
	fmt.Printf("ewma:              %8.2fms\n", ewmaMean)
	fmt.Printf("adaptive-default:  %8.2fms\n", defaultMean)
	fmt.Printf("adaptive-tuned:    %8.2fms\n", tunedMean)
	if defaultMean < ewmaMean && tunedMean < ewmaMean {
		fmt.Println("\nBOTH default and Stage-8-tuned Adaptive beat EWMA at this boundary -- the reversal is")
		fmt.Println("structural to Adaptive's design (load-aware balancing), not an artifact of one specific")
		fmt.Println("hand-chosen weight configuration.")
	} else {
		fmt.Println("\nDefault and tuned Adaptive do NOT both beat EWMA here -- the reversal depends on")
		fmt.Println("configuration, not purely on Adaptive's structural design.")
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=================================================================")
	fmt.Println(" Experiment 013-J: Statistical Confirmation + Default vs Tuned")
	fmt.Println("=================================================================")

	runNearBoundaryConfirmation()
	runDefaultVsTuned()

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Note       string `json:"note"`
	}{Experiment: "013-J-statistical-confirmation-and-tuned-config", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Note: "see stdout for full numeric results; this experiment's primary output is console-printed analysis, not a large structured artifact"}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013J-statistical-confirmation.json"), b, 0644)
	fmt.Println("\nExperiment 013-J complete.")
}
