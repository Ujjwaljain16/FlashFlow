// Command experiment-014d combines two light, targeted Stage 14
// questions at the N=8 near-boundary point found in experiment-014c
// (Requests=381, Capacity=1):
//
// Program E: does the topology-generalized boundary remain visible
// under a transient (FlashCrowd) workload, not just Constant? Only one
// transient pattern is tested, per this stage's own instruction not to
// repeat every Stage 13 workload experiment.
//
// Program F: Stage 13 tested failure only at Capacity=2 with slack, and
// explicitly did not test failure while the system is already near the
// routing boundary. This tests exactly that: does removing capacity via
// failure push an already-marginal system further across the boundary?
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

const outDirName = "experiments/014-scale-topology/results"
const horizon = 4 * time.Second
const capacity = 1
const targetCount = 8

func targets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, targetCount)
	for i := 0; i < targetCount; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: capacity}
	}
	return out
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

type Result struct {
	Scenario string  `json:"scenario"`
	Policy   string  `json:"policy"`
	MeanMs   float64 `json:"mean_ms"`
	P99Ms    float64 `json:"p99_ms"`
}

func meanAndP99(wr *replay.WorldResult) (float64, float64) {
	if len(wr.Completions) == 0 {
		return 0, 0
	}
	ms := make([]float64, len(wr.Completions))
	for i, c := range wr.Completions {
		ms[i] = float64(c.Latency.Microseconds()) / 1000.0
	}
	mean, _ := statistics.Mean(ms)
	p99, _ := statistics.Percentile(ms, 99)
	return mean, p99
}

func runWorkloadPart() []Result {
	fmt.Println("\n=== Program E: Workload Shape at the N=8 Near-Boundary Point ===")
	var results []Result

	constantArrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 381, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, replay.DeriveSeeds(14300).Traffic)
	if err != nil {
		log.Fatalf("constant: generating traffic: %v", err)
	}
	// FlashCrowd with the SAME total request count and horizon, so the
	// TIME-AVERAGED rate matches the constant case exactly -- only the
	// SHAPE (front-loaded, asymmetric rise/decay) differs.
	flashArrivals, err := traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 381, Horizon: horizon, BaseRate: 40, PeakRate: 250,
		BurstAt: 2 * time.Second, BurstWidth: 800 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
	}, replay.DeriveSeeds(14301).Traffic)
	if err != nil {
		log.Fatalf("flash_crowd: generating traffic: %v", err)
	}

	for _, shape := range []struct {
		name     string
		arrivals []replay.Arrival
		seed     int64
	}{{"constant", constantArrivals, 14300}, {"flash_crowd", flashArrivals, 14301}} {
		fmt.Printf("\n-- %s --\n", shape.name)
		scenario := replay.Scenario{Targets: targets(), Arrivals: shape.arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: replay.DeriveSeeds(shape.seed)}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: "014d-workload-" + shape.name, Scenario: scenario}
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
				log.Fatalf("%s/%s: %v", shape.name, ps.name, err)
			}
			mean, p99 := meanAndP99(result.WorldResult)
			r := Result{Scenario: shape.name, Policy: ps.name, MeanMs: mean, P99Ms: p99}
			results = append(results, r)
			fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms\n", ps.name, mean, p99)
		}
	}
	return results
}

func runFailurePart() []Result {
	fmt.Println("\n=== Program F: Failure Interaction AT the Near-Boundary Regime ===")
	var results []Result
	seeds := replay.DeriveSeeds(14302)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 381, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}

	scenarios := []struct {
		name      string
		chaosYAML string
	}{
		{"no_failure", ""},
		{"failure_of_concentrated_target", "- at: 2s\n  target: edge-00\n  action: crash\n- at: 3s\n  target: edge-00\n  action: recover\n"},
		{"failure_of_non_concentrated_target", "- at: 2s\n  target: edge-07\n  action: crash\n- at: 3s\n  target: edge-07\n  action: recover\n"},
	}
	for _, sc := range scenarios {
		fmt.Printf("\n-- %s --\n", sc.name)
		var windows []replay.FailureWindow
		useHealth := false
		if sc.chaosYAML != "" {
			sched, err := chaos.ParseYAML(strings.NewReader(sc.chaosYAML))
			if err != nil {
				log.Fatalf("%s: parsing chaos: %v", sc.name, err)
			}
			windows, err = sched.ToFailureWindows()
			if err != nil {
				log.Fatalf("%s: compiling chaos: %v", sc.name, err)
			}
			useHealth = true
		}
		scenario := replay.Scenario{
			Targets: targets(), Arrivals: arrivals, Failures: windows, UseHealthRegistry: useHealth,
			Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
		}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: "014d-failure-" + sc.name, Scenario: scenario}
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
				log.Fatalf("%s/%s: %v", sc.name, ps.name, err)
			}
			mean, p99 := meanAndP99(result.WorldResult)
			r := Result{Scenario: sc.name, Policy: ps.name, MeanMs: mean, P99Ms: p99}
			results = append(results, r)
			fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms\n", ps.name, mean, p99)
		}
	}
	return results
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=================================================================")
	fmt.Println(" Experiment 014-D: Workload Shape + Failure at the Boundary")
	fmt.Println("=================================================================")

	workload := runWorkloadPart()
	failure := runFailurePart()

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Workload   []Result `json:"workload"`
		Failure    []Result `json:"failure"`
	}{Experiment: "014-D-workload-and-failure-at-boundary", Timestamp: time.Now().UTC().Format(time.RFC3339), Workload: workload, Failure: failure}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014D-workload-and-failure-at-boundary.json"), b, 0644)
	fmt.Println("\nExperiment 014-D complete.")
}
