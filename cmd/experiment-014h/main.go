// Command experiment-014h is Stage 14 Section 23 (Program H): does
// recovery behavior differentiate policies in a generalized (N=8,
// beyond-3-target) topology, the way Stage 13 found it does at N=3?
//
// ONE representative topology (the same N=8 graduated, Capacity=1,
// near-boundary cell used throughout 014c/014d/014f/014g) and THREE
// representative policies spanning the concentration-proneness spectrum
// 014f discovered: ewma (concentration-prone, lost to round-robin at
// this exact cell), least-connections and adaptive (anti-concentration,
// stayed flat). Deliberately not a full recovery matrix, per Section 23's
// own instruction.
//
// Crashes the SAME target (edge-00, the fastest and most commonly
// concentrated-upon target) for a 1s window and measures: pre-failure
// p99, transition (during-failure) p99, post-recovery steady-state p99,
// how long after recovery each policy takes to first re-route to
// edge-00 again, how long edge-00's own queue takes to drain back to
// zero after recovery, and what fraction of during-failure decisions
// wrongly targeted the crashed target (a health-registry correctness
// check, not expected to be non-zero given internal/replay/world.go's
// own `available` filter).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
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
const failAt = 2000.0    // ms
const recoverAt = 3000.0 // ms
const concentratedTarget = "edge-00"

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
		{"ewma", replay.EWMAPolicy()},
		{"least-connections", replay.LeastConnectionsPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

func p99InWindow(wr *replay.WorldResult, fromMs, toMs float64) float64 {
	var ms []float64
	for _, c := range wr.Completions {
		if c.VirtualTimeMs >= fromMs && c.VirtualTimeMs < toMs {
			ms = append(ms, float64(c.Latency.Microseconds())/1000.0)
		}
	}
	if len(ms) == 0 {
		return 0
	}
	p, _ := statistics.Percentile(ms, 99)
	return p
}

// adaptationTimeMs returns how many ms after recoverAt this policy first
// routes a NEW decision to target again, or -1 if it never does within
// the horizon (which is not necessarily a failure -- a policy may simply
// have found another target it now prefers just as much).
func adaptationTimeMs(wr *replay.WorldResult, target string, recoverAt float64) float64 {
	for _, r := range wr.Records {
		if r.VirtualTimeMs >= recoverAt && r.Target == target {
			return r.VirtualTimeMs - recoverAt
		}
	}
	return -1
}

// queueDrainTimeMs reconstructs target's own queue-depth timeline from
// raw dispatch (+1) and completion (-1) events (the same construction
// Track D's own finite-capacity model uses internally), and returns the
// first time at or after afterMs that the running depth returns to zero
// -- i.e. how long the disruption's backlog takes to fully clear. -1 if
// it never reaches zero again within the horizon.
func queueDrainTimeMs(wr *replay.WorldResult, target string, afterMs float64) float64 {
	type event struct {
		t     float64
		delta int
	}
	var events []event
	for _, r := range wr.Records {
		if r.Target == target {
			events = append(events, event{t: r.VirtualTimeMs, delta: 1})
		}
	}
	for _, c := range wr.Completions {
		if c.Target == target {
			events = append(events, event{t: c.VirtualTimeMs, delta: -1})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].t < events[j].t })
	depth := 0
	for _, e := range events {
		depth += e.delta
		if depth == 0 && e.t >= afterMs {
			return e.t - afterMs
		}
	}
	return -1
}

func wrongTargetFraction(wr *replay.WorldResult, target string, fromMs, toMs float64) float64 {
	total, wrong := 0, 0
	for _, r := range wr.Records {
		if r.VirtualTimeMs >= fromMs && r.VirtualTimeMs < toMs {
			total++
			if r.Target == target {
				wrong++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(wrong) / float64(total)
}

type Result struct {
	Policy              string  `json:"policy"`
	PreFailureP99Ms     float64 `json:"pre_failure_p99_ms"`
	TransitionP99Ms     float64 `json:"transition_p99_ms"`
	SteadyStateP99Ms    float64 `json:"steady_state_p99_ms"`
	AdaptationTimeMs    float64 `json:"adaptation_time_ms"`
	QueueDrainTimeMs    float64 `json:"queue_drain_time_ms"`
	WrongTargetFraction float64 `json:"wrong_target_fraction_during_failure"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 014-H: Recovery Policy Differentiation at N=8 (Section 23, Program H)")
	fmt.Println("=====================================================================================")

	seeds := replay.DeriveSeeds(14600)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 381, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}

	chaosYAML := fmt.Sprintf("- at: %gs\n  target: %s\n  action: crash\n- at: %gs\n  target: %s\n  action: recover\n",
		failAt/1000, concentratedTarget, recoverAt/1000, concentratedTarget)
	sched, err := chaos.ParseYAML(strings.NewReader(chaosYAML))
	if err != nil {
		log.Fatalf("parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("compiling chaos: %v", err)
	}

	scenario := replay.Scenario{
		Targets: targets(), Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "014h-recovery", Scenario: scenario}

	var results []Result
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
			log.Fatalf("%s: %v", ps.name, err)
		}
		wr := result.WorldResult
		r := Result{
			Policy:              ps.name,
			PreFailureP99Ms:     p99InWindow(wr, 0, failAt),
			TransitionP99Ms:     p99InWindow(wr, failAt, recoverAt),
			SteadyStateP99Ms:    p99InWindow(wr, recoverAt+500, horizon.Seconds()*1000),
			AdaptationTimeMs:    adaptationTimeMs(wr, concentratedTarget, recoverAt),
			QueueDrainTimeMs:    queueDrainTimeMs(wr, concentratedTarget, recoverAt),
			WrongTargetFraction: wrongTargetFraction(wr, concentratedTarget, failAt, recoverAt),
		}
		results = append(results, r)
		fmt.Printf("  %-18s pre_p99=%8.2fms  transition_p99=%9.2fms  steady_p99=%8.2fms  adapt=%7.1fms  drain=%7.1fms  wrong_target%%=%.3f\n",
			r.Policy, r.PreFailureP99Ms, r.TransitionP99Ms, r.SteadyStateP99Ms, r.AdaptationTimeMs, r.QueueDrainTimeMs, r.WrongTargetFraction)
	}

	fmt.Println("\n--- Health-registry correctness check ---")
	for _, r := range results {
		if r.WrongTargetFraction > 0 {
			fmt.Printf("  %s: %.3f%% of during-failure decisions wrongly targeted the crashed target -- investigate probe-interval lag\n", r.Policy, r.WrongTargetFraction*100)
		}
	}
	fmt.Println("  (0.000 for all policies means the health registry correctly excluded the crashed target from every routing decision)")

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-H-recovery-policy-differentiation", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014H-recovery-policy-differentiation.json"), b, 0644)
	fmt.Println("\nExperiment 014-H complete.")
}
