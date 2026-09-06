// Command experiment-013e is Stage 13 Program E: does the capacity
// boundary depend on SUSTAINED overload, or can a transient burst
// produce the same policy reversal even when the whole-run average rho
// stays under 1? A long-horizon average can hide a short, severe spike --
// this measures phase-level (pre-burst / during-burst / post-burst)
// behavior separately, not just one whole-run number.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/013-regime-discovery/results"
const horizon = 4 * time.Second
const capacity = 1

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
}

type PhaseResult struct {
	Shape       string  `json:"shape"`
	Policy      string  `json:"policy"`
	WholeRunMs  float64 `json:"whole_run_mean_ms"`
	PreBurstMs  float64 `json:"pre_burst_mean_ms"`  // [0, 1.6s)
	BurstMs     float64 `json:"burst_mean_ms"`      // [1.6s, 2.4s) -- around BurstAt=2s, BurstWidth=800ms
	PostBurstMs float64 `json:"post_burst_mean_ms"` // [2.4s, 4s]
}

func meanInWindow(wr *replay.WorldResult, lowMs, highMs float64) float64 {
	var vals []float64
	for _, c := range wr.Completions {
		if c.VirtualTimeMs >= lowMs && c.VirtualTimeMs < highMs {
			vals = append(vals, float64(c.Latency.Microseconds())/1000.0)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	m, _ := statistics.Mean(vals)
	return m
}

func policySpecs() []struct {
	name string
	spec replay.PolicySpec
} {
	return []struct {
		name string
		spec replay.PolicySpec
	}{
		{"round-robin", replay.RoundRobinPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

func runShape(name string, pattern traffic.Pattern, params traffic.Params, rootSeed int64) []PhaseResult {
	seeds := replay.DeriveSeeds(rootSeed)
	arrivals, err := traffic.Generate(pattern, params, seeds.Traffic)
	if err != nil {
		log.Fatalf("%s: generating traffic: %v", name, err)
	}
	scenario := replay.Scenario{Targets: targets(), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "013e-" + name, Scenario: scenario}

	fmt.Printf("\n-- %s --\n", name)
	var out []PhaseResult
	for i, ps := range policySpecs() {
		exp.Policy = ps.spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, ps.spec)
		}
		if err != nil {
			log.Fatalf("%s/%s: %v", name, ps.name, err)
		}
		wr := result.WorldResult
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		whole, _ := statistics.Mean(ms)
		r := PhaseResult{
			Shape: name, Policy: ps.name, WholeRunMs: whole,
			PreBurstMs: meanInWindow(wr, 0, 1600), BurstMs: meanInWindow(wr, 1600, 2400), PostBurstMs: meanInWindow(wr, 2400, 4000),
		}
		out = append(out, r)
		fmt.Printf("  %-14s whole_run=%8.2fms  pre=%8.2fms  during=%8.2fms  post=%8.2fms\n",
			r.Policy, r.WholeRunMs, r.PreBurstMs, r.BurstMs, r.PostBurstMs)
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===================================================================")
	fmt.Println(" Experiment 013-E: Workload Shape -- Sustained vs Transient Overload")
	fmt.Println("===================================================================")

	// Total 3-target system capacity at Capacity=1 each is
	// 1/15ms+1/30ms+1/60ms ~= 116.7 req/s. The "severe" burst below
	// (PeakRate=300) vastly exceeds even that TOTAL capacity regardless
	// of distribution -- a genuine aggregate-overload regime where no
	// routing policy can help. The "mild" burst (PeakRate=100) stays
	// under total capacity, isolating whether a CONCENTRATION-only burst
	// (the mechanism the constant-load finding is actually about) still
	// produces the same Adaptive-favors-balance story under a transient,
	// not sustained, load shape.
	severeCommon := traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 40, PeakRate: 300,
		BurstAt: 2 * time.Second, BurstWidth: 800 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
	}
	mildCommon := traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 40, PeakRate: 100,
		BurstAt: 2 * time.Second, BurstWidth: 800 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
	}
	constantParams := traffic.Params{Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)} // Constant ignores BaseRate/PeakRate entirely

	var all []PhaseResult
	all = append(all, runShape("constant", traffic.Constant, constantParams, 13500)...)
	all = append(all, runShape("burst_severe_peak300", traffic.Burst, severeCommon, 13501)...)
	all = append(all, runShape("flash_crowd_severe_peak300", traffic.FlashCrowd, severeCommon, 13502)...)
	all = append(all, runShape("burst_mild_peak100", traffic.Burst, mildCommon, 13503)...)
	all = append(all, runShape("flash_crowd_mild_peak100", traffic.FlashCrowd, mildCommon, 13504)...)

	out := struct {
		Experiment string        `json:"experiment"`
		Timestamp  string        `json:"timestamp"`
		Results    []PhaseResult `json:"results"`
	}{Experiment: "013-E-workload-shape", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013E-workload-shape.json"), b, 0644)
	fmt.Println("\nExperiment 013-E complete.")
}
