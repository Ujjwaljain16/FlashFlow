// Command experiment-012f re-runs Stage 11 Program C's recovery-dynamics
// scenario under Stage 12's contention model. Stage 11's own finding
// (docs/StageArtifacts/Stage11.md §13) was that transition_p99 was
// IDENTICAL to steady_state_p99 for every policy (exactly 1.00x),
// because the flat model bounds any wrong decision's cost at one fixed
// per-target service time -- no queue buildup, no compounding delay.
// The question here: does Capacity>0 let a real "adaptation cost" show
// up in this metric for the first time?
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

const outDirName = "experiments/012-model-fidelity/results"

const (
	requests         = 550
	horizon          = 7500 * time.Millisecond
	transitionWindow = 300 * time.Millisecond
)

var phaseBoundariesMs = []float64{1500, 3000, 4500, 6000}

type PhaseMetrics struct {
	Policy           string  `json:"policy"`
	Capacity         int     `json:"capacity"`
	Completed        int     `json:"completed"`
	SteadyStateP99Ms float64 `json:"steady_state_p99_ms"`
	TransitionP99Ms  float64 `json:"transition_p99_ms"`
	Ratio            float64 `json:"transition_steady_state_ratio"`
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

func inTransitionWindow(tMs float64) bool {
	for _, boundary := range phaseBoundariesMs {
		if tMs >= boundary && tMs < boundary+float64(transitionWindow.Milliseconds()) {
			return true
		}
	}
	return false
}

func runPhases(capacity int) []PhaseMetrics {
	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 45 * time.Millisecond, Capacity: capacity},
	}
	seeds := replay.DeriveSeeds(3013) // identical root seed to Stage 11's own Program C
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requests, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.3),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("capacity=%d: generating traffic: %v", capacity, err)
	}
	sched, err := chaos.ParseYAML(strings.NewReader(strings.Join([]string{
		"- at: 1.5s\n  target: edge-a\n  action: crash",
		"- at: 3s\n  target: edge-c\n  action: crash",
		"- at: 4.5s\n  target: edge-a\n  action: recover",
		"- at: 6s\n  target: edge-c\n  action: recover",
	}, "\n") + "\n"))
	if err != nil {
		log.Fatalf("parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("compiling chaos: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: fmt.Sprintf("012f-capacity%d", capacity), Scenario: scenario}

	var out []PhaseMetrics
	fmt.Printf("\n-- Capacity=%d --\n", capacity)
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
			log.Fatalf("capacity=%d, %s: %v", capacity, ps.name, err)
		}
		wr := result.WorldResult

		var steady, transition []float64
		for _, c := range wr.Completions {
			ms := float64(c.Latency.Microseconds()) / 1000.0
			if inTransitionWindow(c.VirtualTimeMs) {
				transition = append(transition, ms)
			} else {
				steady = append(steady, ms)
			}
		}
		m := PhaseMetrics{Policy: ps.name, Capacity: capacity, Completed: len(wr.Completions)}
		if len(steady) > 0 {
			m.SteadyStateP99Ms, _ = statistics.Percentile(steady, 99)
		}
		if len(transition) > 0 {
			m.TransitionP99Ms, _ = statistics.Percentile(transition, 99)
		}
		if m.SteadyStateP99Ms > 0 {
			m.Ratio = m.TransitionP99Ms / m.SteadyStateP99Ms
		}
		out = append(out, m)
		fmt.Printf("  %-14s completed=%-4d steady-state p99=%8.2fms  transition p99=%8.2fms  ratio=%.2fx\n",
			m.Policy, m.Completed, m.SteadyStateP99Ms, m.TransitionP99Ms, m.Ratio)
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===========================================================")
	fmt.Println(" Experiment 012-F: Recovery Dynamics Re-run Under Contention")
	fmt.Println("===========================================================")

	var all []PhaseMetrics
	all = append(all, runPhases(0)...) // flat, matches Stage 11 exactly
	all = append(all, runPhases(2)...) // contention-enabled (2 slots -- 1 would starve this 3-phase, higher-intensity scenario too severely to isolate a transition-specific effect)

	out := struct {
		Experiment string         `json:"experiment"`
		Timestamp  string         `json:"timestamp"`
		Phases     []PhaseMetrics `json:"phases"`
	}{Experiment: "012-F-recovery-dynamics-under-contention", Timestamp: time.Now().UTC().Format(time.RFC3339), Phases: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "012F-recovery-dynamics-under-contention.json"), b, 0644)
	fmt.Println("\nExperiment 012-F complete.")
}
