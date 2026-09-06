// Command experiment-013i combines two related Stage 13 questions:
//
// Program I: does Stage 12's ~1.20x transition/steady-state p99 ratio
// (uniform across round-robin/ewma/adaptive at Capacity=2 on one
// topology) generalize to a DIFFERENT capacity level and the FULL
// 6-policy set, or was uniformity itself specific to those three
// policies?
//
// Program J: expand from the EWMA/Adaptive-only investigation to the
// full policy set (round-robin, weighted-round-robin, least-connections,
// ewma, p2c-load, adaptive) on the flagship capacity-boundary scenario,
// asking how many DISTINCT routing regimes exist -- not running an
// exhaustive factorial matrix, but checking whether the 6 policies
// cluster into a small number of qualitatively different behaviors.
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

func policySpecs() []struct {
	name string
	spec replay.PolicySpec
} {
	return []struct {
		name string
		spec replay.PolicySpec
	}{
		{"round-robin", replay.RoundRobinPolicy()},
		{"weighted-round-robin", replay.WeightedRoundRobinPolicy()},
		{"least-connections", replay.LeastConnectionsPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"p2c-load", replay.P2CLoadPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

// --- Program J: multi-policy regime map on the flagship boundary scenario ---

type RegimeCell struct {
	Capacity int     `json:"capacity"`
	Policy   string  `json:"policy"`
	MeanMs   float64 `json:"mean_ms"`
	MaxShare float64 `json:"max_share"`
}

func runRegimeMap() []RegimeCell {
	fmt.Println("\n=== Program J: Multi-Policy Regime Map (flagship scenario) ===")
	targets := func(capacity int) []replay.TargetProfile {
		return []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
			{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
		}
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(1001) // identical to Stage 11/12's own flagship
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}

	var cells []RegimeCell
	for _, capacity := range []int{0, 1} {
		fmt.Printf("\n-- Capacity=%d --\n", capacity)
		tgts := targets(capacity)
		scenario := replay.Scenario{Targets: tgts, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013i-regime-cap%d", capacity), Scenario: scenario}
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
				log.Fatalf("cap=%d/%s: %v", capacity, ps.name, err)
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
			cell := RegimeCell{Capacity: capacity, Policy: ps.name, MeanMs: mean, MaxShare: maxShare}
			cells = append(cells, cell)
			fmt.Printf("  %-22s mean=%9.2fms  max_share=%.3f\n", ps.name, mean, maxShare)
		}
	}
	return cells
}

// --- Program I: recovery-dynamics transition ratio, full policy set, 2 capacities ---

const (
	requests         = 550
	recoveryHorizon  = 7500 * time.Millisecond
	transitionWindow = 300 * time.Millisecond
)

var phaseBoundariesMs = []float64{1500, 3000, 4500, 6000}

func inTransitionWindow(tMs float64) bool {
	for _, boundary := range phaseBoundariesMs {
		if tMs >= boundary && tMs < boundary+float64(transitionWindow.Milliseconds()) {
			return true
		}
	}
	return false
}

type RecoveryCell struct {
	Capacity  int     `json:"capacity"`
	Policy    string  `json:"policy"`
	SteadyP99 float64 `json:"steady_state_p99_ms"`
	TransP99  float64 `json:"transition_p99_ms"`
	Ratio     float64 `json:"ratio"`
}

func runRecoveryGeneralization() []RecoveryCell {
	fmt.Println("\n=== Program I: Recovery-Dynamics Transition Ratio, Full Policy Set ===")
	targets := func(capacity int) []replay.TargetProfile {
		return []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
			{Name: "edge-c", ServiceTime: 45 * time.Millisecond, Capacity: capacity},
		}
	}
	seeds := replay.DeriveSeeds(3013) // identical to Stage 12's own recovery-dynamics root seed
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requests, Horizon: recoveryHorizon, KeyFunc: traffic.HotColdKeys(0.3),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
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

	var cells []RecoveryCell
	for _, capacity := range []int{0, 3} { // 0 = flat (matches Stage 12 exactly); 3 = a DIFFERENT contention level than Stage 12's own Capacity=2
		fmt.Printf("\n-- Capacity=%d --\n", capacity)
		tgts := targets(capacity)
		scenario := replay.Scenario{
			Targets: tgts, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
			Horizon: clock.VirtualTime(recoveryHorizon.Nanoseconds()), Seeds: seeds,
		}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013i-recovery-cap%d", capacity), Scenario: scenario}
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
				log.Fatalf("cap=%d/%s: %v", capacity, ps.name, err)
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
			cell := RecoveryCell{Capacity: capacity, Policy: ps.name}
			if len(steady) > 0 {
				cell.SteadyP99, _ = statistics.Percentile(steady, 99)
			}
			if len(transition) > 0 {
				cell.TransP99, _ = statistics.Percentile(transition, 99)
			}
			if cell.SteadyP99 > 0 {
				cell.Ratio = cell.TransP99 / cell.SteadyP99
			}
			cells = append(cells, cell)
			fmt.Printf("  %-22s steady_p99=%9.2fms  transition_p99=%9.2fms  ratio=%.2fx\n", ps.name, cell.SteadyP99, cell.TransP99, cell.Ratio)
		}
	}
	return cells
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("====================================================================")
	fmt.Println(" Experiment 013-I: Recovery Generalization + Multi-Policy Regime Map")
	fmt.Println("====================================================================")

	regimeCells := runRegimeMap()
	recoveryCells := runRecoveryGeneralization()

	out := struct {
		Experiment    string         `json:"experiment"`
		Timestamp     string         `json:"timestamp"`
		RegimeMap     []RegimeCell   `json:"regime_map"`
		RecoveryCells []RecoveryCell `json:"recovery_cells"`
	}{Experiment: "013-I-recovery-and-multipolicy", Timestamp: time.Now().UTC().Format(time.RFC3339), RegimeMap: regimeCells, RecoveryCells: recoveryCells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013I-recovery-and-multipolicy.json"), b, 0644)
	fmt.Println("\nExperiment 013-I complete.")
}
