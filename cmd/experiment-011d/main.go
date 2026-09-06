// Command experiment-011d is Stage 11's Program D: distribution shift.
// "Development" is one of Program A's own regime-map configurations
// (moderate heterogeneity, constant workload, no failure) -- a case
// where EWMA beat Adaptive by roughly a third. "Evaluation" changes
// MULTIPLE generating parameters at once (heterogeneity severity,
// workload shape, key skew, failure presence, and intensity), not just
// the traffic seed -- Stage 10's own audit already flagged that a
// disjoint seed range from the SAME generator is not a distribution
// shift, only a different sample from the same one (see Stage11.md's
// own H5 and Stage10.md's Holdout callout). H5 asks whether Adaptive's
// disadvantage narrows, widens, or reverses under genuine shift -- this
// program measures it rather than assumes a direction.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/011-research-validation/results"

type DistributionResult struct {
	Distribution string  `json:"distribution"`
	Policy       string  `json:"policy"`
	MeanMs       float64 `json:"mean_ms"`
	P99Ms        float64 `json:"p99_ms"`
	MaxShare     float64 `json:"max_share"`
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

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=========================================================")
	fmt.Println(" Experiment 011-D: Distribution Shift")
	fmt.Println("=========================================================")

	dev := runDistribution("development", []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 20 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 40 * time.Millisecond},
	}, traffic.Constant, traffic.Params{
		Requests: 300, Horizon: 4 * time.Second, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
	}, "", 4001)

	// Evaluation distribution changes FIVE things at once relative to
	// development, deliberately: heterogeneity severity (2x -> 6x spread),
	// workload shape (constant -> flash crowd), key skew (0.5 -> 0.85,
	// much hotter), intensity (75req/s base -> 150 peak), AND introduces a
	// failure timed during the flash-crowd peak (none -> during-transition)
	// -- a combination, not a single-seed reshuffle.
	shifted := runDistribution("evaluation-shifted", []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 10 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}, traffic.FlashCrowd, traffic.Params{
		Requests: 300, Horizon: 4 * time.Second, BaseRate: 30, PeakRate: 150,
		BurstAt: 2 * time.Second, BurstWidth: time.Second, KeyFunc: traffic.HotColdKeys(0.85),
	}, "- at: 1.8s\n  target: edge-a\n  action: crash\n- at: 2.3s\n  target: edge-a\n  action: recover\n", 4002)

	fmt.Println("\n--- Distribution-shift comparison ---")
	fmt.Println("policy         dev mean   dev margin*   shifted mean   shifted margin*")
	devByPolicy := indexByPolicy(dev)
	shiftedByPolicy := indexByPolicy(shifted)
	devBest := bestMean(dev)
	shiftedBest := bestMean(shifted)
	for _, ps := range policySpecs() {
		d := devByPolicy[ps.name]
		s := shiftedByPolicy[ps.name]
		devMargin := (d.MeanMs - devBest) / devBest * 100
		shiftedMargin := (s.MeanMs - shiftedBest) / shiftedBest * 100
		fmt.Printf("%-14s %8.2fms %11.1f%%  %12.2fms %13.1f%%\n", ps.name, d.MeanMs, devMargin, s.MeanMs, shiftedMargin)
	}
	fmt.Println("(*margin = % worse than the best policy in that distribution; 0.0% means it won)")

	adaptDevMargin := (devByPolicy["adaptive"].MeanMs - devBest) / devBest * 100
	adaptShiftedMargin := (shiftedByPolicy["adaptive"].MeanMs - shiftedBest) / shiftedBest * 100
	fmt.Printf("\nAdaptive's margin behind the best policy: development=%.1f%%  shifted=%.1f%%\n", adaptDevMargin, adaptShiftedMargin)
	switch {
	case adaptShiftedMargin < adaptDevMargin-1:
		fmt.Println("Adaptive's disadvantage NARROWED under distribution shift.")
	case adaptShiftedMargin > adaptDevMargin+1:
		fmt.Println("Adaptive's disadvantage WIDENED under distribution shift.")
	default:
		fmt.Println("Adaptive's disadvantage is UNCHANGED (within 1pp) under distribution shift.")
	}

	all := append(dev, shifted...)
	out := struct {
		Experiment string               `json:"experiment"`
		Timestamp  string               `json:"timestamp"`
		Results    []DistributionResult `json:"results"`
	}{Experiment: "011-D-distribution-shift", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011D-distribution-shift.json"), b, 0644)
	fmt.Println("\nExperiment 011-D complete.")
}

func indexByPolicy(rs []DistributionResult) map[string]DistributionResult {
	m := make(map[string]DistributionResult, len(rs))
	for _, r := range rs {
		m[r.Policy] = r
	}
	return m
}

func bestMean(rs []DistributionResult) float64 {
	best := rs[0].MeanMs
	for _, r := range rs {
		if r.MeanMs < best {
			best = r.MeanMs
		}
	}
	return best
}

func runDistribution(name string, targets []replay.TargetProfile, pattern traffic.Pattern, params traffic.Params, chaosYAML string, rootSeed int64) []DistributionResult {
	seeds := replay.DeriveSeeds(rootSeed)
	arrivals, err := traffic.Generate(pattern, params, seeds.Traffic)
	if err != nil {
		log.Fatalf("%s: generating traffic: %v", name, err)
	}
	var windows []replay.FailureWindow
	useHealth := false
	if chaosYAML != "" {
		sched, err := chaos.ParseYAML(strings.NewReader(chaosYAML))
		if err != nil {
			log.Fatalf("%s: parsing chaos: %v", name, err)
		}
		windows, err = sched.ToFailureWindows()
		if err != nil {
			log.Fatalf("%s: compiling chaos: %v", name, err)
		}
		useHealth = true
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: useHealth,
		Horizon: clock.VirtualTime(params.Horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "011d-" + name, Scenario: scenario}

	var out []DistributionResult
	fmt.Printf("\n-- %s distribution --\n", name)
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
			log.Fatalf("%s %s: %v", name, ps.name, err)
		}
		wr := result.WorldResult
		r := DistributionResult{Distribution: name, Policy: ps.name}
		if len(wr.Completions) > 0 {
			ms := make([]float64, len(wr.Completions))
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			r.MeanMs, _ = statistics.Mean(ms)
			r.P99Ms, _ = statistics.Percentile(ms, 99)
			maxCount := 0
			for _, c := range wr.CompletedByTarget {
				if c > maxCount {
					maxCount = c
				}
			}
			r.MaxShare = float64(maxCount) / float64(len(wr.Completions))
		}
		out = append(out, r)
		fmt.Printf("  %-14s mean=%.2fms  p99=%.2fms  max_share=%.3f\n", ps.name, r.MeanMs, r.P99Ms, r.MaxShare)
	}
	return out
}
