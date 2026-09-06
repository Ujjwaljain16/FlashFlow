// Command experiment-013f is Stage 13 Program F: does removing capacity
// through FAILURE move the policy boundary the same way removing it
// through a literal Capacity reduction does? Treats the failure schedule
// as an exogenous intervention on available system capacity, not a
// generic "bad condition" -- when a target fails, the system's TOTAL
// capacity drops, which should push remaining targets' effective rho up
// exactly the way a lower Capacity value would.
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

const outDirName = "experiments/013-regime-discovery/results"
const horizon = 4 * time.Second
const capacity = 2 // chosen because experiment-013a found this level lets EWMA fully recover with NO failure -- if failure moves the boundary, it should push EWMA back toward collapse

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
}

type Result struct {
	Scenario string  `json:"scenario"`
	Policy   string  `json:"policy"`
	MeanMs   float64 `json:"mean_ms"`
	MaxShare float64 `json:"max_share"`
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

func run(name, chaosYAML string, rootSeed int64) []Result {
	seeds := replay.DeriveSeeds(rootSeed)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
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
		Targets: targets(), Arrivals: arrivals, Failures: windows, UseHealthRegistry: useHealth,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "013f-" + name, Scenario: scenario}

	fmt.Printf("\n-- %s --\n", name)
	var out []Result
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
		mean, _ := statistics.Mean(ms)
		maxCount := 0
		for _, c := range wr.CompletedByTarget {
			if c > maxCount {
				maxCount = c
			}
		}
		maxShare := 0.0
		if len(wr.Completions) > 0 {
			maxShare = float64(maxCount) / float64(len(wr.Completions))
		}
		r := Result{Scenario: name, Policy: ps.name, MeanMs: mean, MaxShare: maxShare}
		out = append(out, r)
		fmt.Printf("  %-14s mean=%9.2fms  max_share=%.3f\n", r.Policy, r.MeanMs, r.MaxShare)
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=======================================================")
	fmt.Println(" Experiment 013-F: Failure as Capacity Removal")
	fmt.Println("=======================================================")

	var all []Result
	all = append(all, run("no_failure", "", 13600)...)
	all = append(all, run("single_failure_mid_run", "- at: 2s\n  target: edge-b\n  action: crash\n- at: 3s\n  target: edge-b\n  action: recover\n", 13601)...)
	all = append(all, run("failure_of_slowest_target", "- at: 2s\n  target: edge-c\n  action: crash\n- at: 3s\n  target: edge-c\n  action: recover\n", 13602)...)
	// Failing edge-a itself (the target EWMA concentrates onto) removes
	// the ONLY remaining stable option and forces a redistribution onto
	// slower targets -- the most severe capacity-removal case tested.
	all = append(all, run("failure_of_fastest_target", "- at: 2s\n  target: edge-a\n  action: crash\n- at: 3s\n  target: edge-a\n  action: recover\n", 13603)...)

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "013-F-failure-as-capacity-removal", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013F-failure-as-capacity-removal.json"), b, 0644)
	fmt.Println("\nExperiment 013-F complete.")
}
