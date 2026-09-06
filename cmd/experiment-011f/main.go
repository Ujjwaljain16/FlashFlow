// Command experiment-011f is Stage 11's Program F: virtual-vs-real
// validation, run directly against Program A's single sharpest finding
// (cmd/experiment-011a, severe/constant/none) rather than a generic
// sweep -- Program A found EWMA winning on mean latency by concentrating
// 97% of traffic onto the fastest target, a virtual engine with no
// queueing/contention model (internal/replay/world.go:200-201) unable to
// penalize that concentration. The only way to know whether that ranking
// is a real routing property or a modeling artifact is to run the
// identical topology and traffic pattern through the real engine, which
// dispatches genuine concurrent HTTP requests against real net/http
// servers, and see whether EWMA's advantage and its concentration both
// survive.
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
	"flashflow/internal/traffic"
)

const outDirName = "experiments/011-research-validation/results"

const (
	requests = 300
	horizon  = 4 * time.Second
)

// severeTargets mirrors experiment-011a's "severe" heterogeneity level
// exactly (15ms/30ms/60ms), so this comparison is against the identical
// topology that produced the striking Program A result, not a fresh one.
func severeTargets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
}

func trafficParams() traffic.Params {
	return traffic.Params{Requests: requests, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5)}
}

// uniqueKeyTrafficParams is an ablation variant: every request carries a
// distinct key, so AdaptiveSelector.scoreCache is 0 for every target on
// every request (no request key is ever repeated, so keyAffinity never
// has a hit) -- isolating whether cache affinity is the mechanism behind
// the 100%-concentration lock-in observed on the real engine, or whether
// load/latency dynamics alone are sufficient to produce it.
func uniqueKeyTrafficParams() traffic.Params {
	p := trafficParams()
	p.KeyFunc = func(i int) string { return fmt.Sprintf("/req%d", i) }
	return p
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

type EngineResult struct {
	Engine         string         `json:"engine"`
	Policy         string         `json:"policy"`
	Completed      int            `json:"completed"`
	MaxShare       float64        `json:"max_share"`
	SharesByTarget map[string]int `json:"shares_by_target"`
	P50Ms          float64        `json:"p50_ms"`
	P99Ms          float64        `json:"p99_ms"`
	MeanMs         float64        `json:"mean_ms,omitempty"` // virtual only -- the real engine's histogram has no direct mean accessor
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("===================================================================")
	fmt.Println(" Experiment 011-F: Virtual vs Real Validation")
	fmt.Println(" scenario: severe heterogeneity, constant workload, no failure")
	fmt.Println(" (the exact configuration behind 011-A's sharpest finding)")
	fmt.Println("===================================================================")

	rootSeed := int64(2011)
	seeds := replay.DeriveSeeds(rootSeed)

	var results []EngineResult

	// --- Virtual engine ---
	arrivals, err := traffic.Generate(traffic.Constant, trafficParams(), seeds.Traffic)
	if err != nil {
		log.Fatalf("generating virtual traffic: %v", err)
	}
	scenario := replay.Scenario{
		Targets:  severeTargets(),
		Arrivals: arrivals,
		Horizon:  clock.VirtualTime(horizon.Nanoseconds()),
		Seeds:    seeds,
	}
	v := engine.NewVirtualEngine()
	vExp := engine.Experiment{ID: "011f-virtual", Scenario: scenario}

	fmt.Println("\n-- Virtual engine --")
	for i, ps := range policySpecs() {
		vExp.Policy = ps.spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(vExp)
		} else {
			result, err = v.Replay(vExp, ps.spec)
		}
		if err != nil {
			log.Fatalf("virtual run, policy %s: %v", ps.name, err)
		}
		r := virtualMetrics(ps.name, result.WorldResult)
		results = append(results, r)
		fmt.Printf("  %-14s completed=%-4d max_share=%.3f  p50=%.2fms  p99=%.2fms  mean=%.2fms\n",
			r.Policy, r.Completed, r.MaxShare, r.P50Ms, r.P99Ms, r.MeanMs)
	}

	// --- Real engine ---
	edges := map[string]time.Duration{"edge-a": 15 * time.Millisecond, "edge-b": 30 * time.Millisecond, "edge-c": 60 * time.Millisecond}
	realCfg := &engine.RealExperimentConfig{
		Edges:          edges,
		TrafficPattern: traffic.Constant,
		TrafficParams:  trafficParams(),
	}
	r := engine.NewRealEngine()

	fmt.Println("\n-- Real engine (genuine concurrent HTTP dispatch, ~4s wall-clock per run) --")
	for i, ps := range policySpecs() {
		rExp := engine.Experiment{ID: "011f-real", Scenario: scenario, Policy: ps.spec, Real: realCfg}
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = r.Run(rExp)
		} else {
			result, err = r.Replay(rExp, ps.spec)
		}
		if err != nil {
			log.Fatalf("real run, policy %s: %v", ps.name, err)
		}
		rm := realMetrics(ps.name, result.Real)
		results = append(results, rm)
		fmt.Printf("  %-14s completed=%-4d max_share=%.3f  p50=%.2fms  p99=%.2fms\n",
			rm.Policy, rm.Completed, rm.MaxShare, rm.P50Ms, rm.P99Ms)
	}

	summarizeAgreement(results)

	// --- Ablation: does removing cache-key repetition change the real
	// engine's lock-in behavior? Same topology/rate, distinct key per
	// request, ewma+adaptive only (round-robin has no signal to lock onto).
	fmt.Println("\n-- Ablation: real engine, unique key per request (no cache-affinity signal) --")
	ablationCfg := &engine.RealExperimentConfig{
		Edges:          edges,
		TrafficPattern: traffic.Constant,
		TrafficParams:  uniqueKeyTrafficParams(),
	}
	uniqueArrivals, err := traffic.Generate(traffic.Constant, uniqueKeyTrafficParams(), seeds.Traffic)
	if err != nil {
		log.Fatalf("generating unique-key virtual traffic: %v", err)
	}
	ablationScenario := scenario
	ablationScenario.Arrivals = uniqueArrivals
	var ablationResults []EngineResult
	for i, ps := range []struct {
		name string
		spec replay.PolicySpec
	}{{"ewma", replay.EWMAPolicy()}, {"adaptive", replay.AdaptivePolicy()}} {
		rExp := engine.Experiment{ID: "011f-ablation", Scenario: ablationScenario, Policy: ps.spec, Real: ablationCfg}
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = r.Run(rExp)
		} else {
			result, err = r.Replay(rExp, ps.spec)
		}
		if err != nil {
			log.Fatalf("ablation run, policy %s: %v", ps.name, err)
		}
		rm := realMetrics(ps.name, result.Real)
		rm.Engine = "real-ablation-unique-keys"
		ablationResults = append(ablationResults, rm)
		fmt.Printf("  %-14s completed=%-4d max_share=%.3f  p50=%.2fms  p99=%.2fms\n",
			rm.Policy, rm.Completed, rm.MaxShare, rm.P50Ms, rm.P99Ms)
	}
	results = append(results, ablationResults...)

	out := struct {
		Experiment string         `json:"experiment"`
		Timestamp  string         `json:"timestamp"`
		Results    []EngineResult `json:"results"`
	}{Experiment: "011-F-virtual-vs-real", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011F-virtual-vs-real.json"), b, 0644)

	fmt.Println("\nExperiment 011-F complete.")
}

func virtualMetrics(policy string, wr *replay.WorldResult) EngineResult {
	r := EngineResult{Engine: "virtual", Policy: policy, SharesByTarget: wr.CompletedByTarget}
	r.Completed = len(wr.Completions)
	if r.Completed == 0 {
		return r
	}
	lat := make([]float64, len(wr.Completions))
	var sum float64
	for i, c := range wr.Completions {
		ms := float64(c.Latency.Microseconds()) / 1000.0
		lat[i] = ms
		sum += ms
	}
	r.MeanMs = sum / float64(len(lat))
	r.P50Ms = percentileMs(lat, 50)
	r.P99Ms = percentileMs(lat, 99)
	maxCount := 0
	for _, c := range wr.CompletedByTarget {
		if c > maxCount {
			maxCount = c
		}
	}
	r.MaxShare = float64(maxCount) / float64(r.Completed)
	return r
}

func realMetrics(policy string, rm *engine.RealMetrics) EngineResult {
	r := EngineResult{Engine: "real", Policy: policy}
	r.Completed = rm.Requests
	if rm.Metrics.Histogram != nil {
		r.P50Ms = float64(rm.Metrics.Histogram.ValueAtPercentile(50)) / 1e6
		r.P99Ms = float64(rm.Metrics.Histogram.ValueAtPercentile(99)) / 1e6
	}
	r.SharesByTarget = make(map[string]int, len(rm.Metrics.RequestsTotal))
	maxCount := 0
	total := 0
	for target, count := range rm.Metrics.RequestsTotal {
		r.SharesByTarget[target] = int(count)
		total += int(count)
		if int(count) > maxCount {
			maxCount = int(count)
		}
	}
	if total > 0 {
		r.MaxShare = float64(maxCount) / float64(total)
	}
	return r
}

// percentileMs is a minimal nearest-rank percentile over a sorted-in-place
// copy -- kept local rather than pulling in internal/statistics.Percentile
// so this file's two metrics functions stay symmetric (the real engine's
// histogram has its own percentile method with different semantics; using
// two different percentile implementations for the two engines would
// itself be a methodology inconsistency worth avoiding).
func percentileMs(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	idx := int(p/100*float64(len(sorted))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func summarizeAgreement(results []EngineResult) {
	byEnginePolicy := map[string]EngineResult{}
	for _, r := range results {
		byEnginePolicy[r.Engine+"/"+r.Policy] = r
	}
	fmt.Println("\n--- Agreement check ---")
	fmt.Println("policy         virtual p50   real p50   virtual max_share   real max_share")
	for _, ps := range policySpecs() {
		v := byEnginePolicy["virtual/"+ps.name]
		r := byEnginePolicy["real/"+ps.name]
		fmt.Printf("%-14s %10.2fms %9.2fms %18.3f %16.3f\n", ps.name, v.P50Ms, r.P50Ms, v.MaxShare, r.MaxShare)
	}

	vBest, rBest := "", ""
	vBestP50, rBestP50 := 1e18, 1e18
	for _, ps := range policySpecs() {
		if v := byEnginePolicy["virtual/"+ps.name]; v.P50Ms < vBestP50 {
			vBestP50, vBest = v.P50Ms, ps.name
		}
		if r := byEnginePolicy["real/"+ps.name]; r.P50Ms < rBestP50 {
			rBestP50, rBest = r.P50Ms, ps.name
		}
	}
	fmt.Printf("\nVirtual engine's lowest-p50 policy: %s\nReal engine's lowest-p50 policy:    %s\n", vBest, rBest)
	if vBest == rBest {
		fmt.Println("RANKING AGREES between engines for this scenario.")
	} else {
		fmt.Println("RANKING DISAGREES between engines for this scenario -- Program A's virtual-only finding does NOT directly transfer here.")
	}
}
