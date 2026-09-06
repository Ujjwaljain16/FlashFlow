// Command experiment-011c is Stage 11's Program C: recovery and
// adaptation dynamics through a multi-phase scenario, measuring
// transition behavior directly rather than only steady-state averages.
//
// The assignment's own phrasing ("target A best -> B best -> C fails ->
// A recovers -> capacity changes again") is expressed here through
// failure/recovery windows, not through a time-varying service time --
// internal/replay.TargetProfile has no such mechanism (see
// cmd/experiment-011b's own disclosed capability gap for H2, the same
// underlying limitation). Concretely:
//
//	phase 1 (0.0-1.5s): all 3 up, edge-a (fastest) is naturally best
//	phase 2 (1.5-3.0s): edge-a down -- edge-b (next fastest) is best of what's available
//	phase 3 (3.0-4.5s): edge-c ALSO down -- only edge-b remains ("C fails")
//	phase 4 (4.5-6.0s): edge-a recovers -- best-of-available reverts toward edge-a
//	phase 5 (6.0-7.5s): edge-c recovers too -- full topology restored ("capacity changes again")
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

const (
	requests = 550
	horizon  = 7500 * time.Millisecond
	// transitionWindow: how long after a phase boundary counts as
	// "adapting" rather than "steady state" -- 300ms is comfortably more
	// than a handful of request inter-arrival gaps at this rate (75 req/s
	// -> ~13ms mean gap) so a transition window reliably contains several
	// decisions, not zero or one.
	transitionWindow = 300 * time.Millisecond
)

var phaseBoundariesMs = []float64{1500, 3000, 4500, 6000}

type PhaseMetrics struct {
	Policy             string  `json:"policy"`
	Completed          int     `json:"completed"`
	Rejected           int     `json:"rejected"`
	SteadyStateP99Ms   float64 `json:"steady_state_p99_ms"` // p99 over all requests NOT in a transition window
	TransitionP99Ms    float64 `json:"transition_p99_ms"`   // p99 over requests within transitionWindow after any phase boundary
	TransitionRequests int     `json:"transition_requests"`
	SteadyStateN       int     `json:"steady_state_requests"`
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
		{"weighted-round-robin", replay.WeightedRoundRobinPolicy()},
		{"least-connections", replay.LeastConnectionsPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"p2c-load", replay.P2CLoadPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("==================================================================")
	fmt.Println(" Experiment 011-C: Recovery and Adaptation Dynamics")
	fmt.Println(" phases: A-best -> A-down/B-best -> C-also-down -> A-recovers -> C-recovers")
	fmt.Println("==================================================================")

	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 45 * time.Millisecond},
	}
	seeds := replay.DeriveSeeds(3013)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requests, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.3),
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
		log.Fatalf("parsing chaos schedule: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("compiling chaos schedule: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "011c-recovery-dynamics", Scenario: scenario}

	var all []PhaseMetrics
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
		m := computeMetrics(ps.name, result.WorldResult)
		all = append(all, m)
		fmt.Printf("  %-22s completed=%-4d rejected=%-4d  steady-state p99=%7.2fms (n=%d)  transition p99=%7.2fms (n=%d)\n",
			m.Policy, m.Completed, m.Rejected, m.SteadyStateP99Ms, m.SteadyStateN, m.TransitionP99Ms, m.TransitionRequests)
	}

	fmt.Println("\nTransition/steady-state ratio (>1 means the policy pays a real cost while adapting):")
	for _, m := range all {
		ratio := 0.0
		if m.SteadyStateP99Ms > 0 {
			ratio = m.TransitionP99Ms / m.SteadyStateP99Ms
		}
		fmt.Printf("  %-22s %.2fx\n", m.Policy, ratio)
	}

	fmt.Println("\nPer-phase mean latency by policy (finer-grained than the aggregate ratio above):")
	fmt.Println("policy                 phase1(A-best)  phase2(A-down)  phase3(A+C-down)  phase4(A-back)  phase5(all-up)")
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
			log.Fatalf("%s (phase pass): %v", ps.name, err)
		}
		means := perPhaseMean(result.WorldResult)
		fmt.Printf("%-22s %14.2f  %14.2f  %16.2f  %14.2f  %14.2f\n", ps.name, means[0], means[1], means[2], means[3], means[4])
	}

	out := struct {
		Experiment string         `json:"experiment"`
		Timestamp  string         `json:"timestamp"`
		Phases     []PhaseMetrics `json:"phases"`
	}{Experiment: "011-C-recovery-dynamics", Timestamp: time.Now().UTC().Format(time.RFC3339), Phases: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011C-recovery-dynamics.json"), b, 0644)
	fmt.Println("\nExperiment 011-C complete.")
}

func inTransitionWindow(tMs float64) bool {
	for _, boundary := range phaseBoundariesMs {
		if tMs >= boundary && tMs < boundary+float64(transitionWindow.Milliseconds()) {
			return true
		}
	}
	return false
}

// phaseIndex returns which of the 5 phases (0-4) a completion at tMs
// falls into, given phaseBoundariesMs = [1500, 3000, 4500, 6000].
func phaseIndex(tMs float64) int {
	for i, b := range phaseBoundariesMs {
		if tMs < b {
			return i
		}
	}
	return len(phaseBoundariesMs)
}

func perPhaseMean(wr *replay.WorldResult) [5]float64 {
	var sums [5]float64
	var counts [5]int
	for _, c := range wr.Completions {
		i := phaseIndex(c.VirtualTimeMs)
		sums[i] += float64(c.Latency.Microseconds()) / 1000.0
		counts[i]++
	}
	var means [5]float64
	for i := range sums {
		if counts[i] > 0 {
			means[i] = sums[i] / float64(counts[i])
		}
	}
	return means
}

func computeMetrics(policy string, wr *replay.WorldResult) PhaseMetrics {
	m := PhaseMetrics{Policy: policy}
	m.Completed = len(wr.Completions)
	m.Rejected = wr.RejectedCount

	var steady, transition []float64
	for _, c := range wr.Completions {
		ms := float64(c.Latency.Microseconds()) / 1000.0
		if inTransitionWindow(c.VirtualTimeMs) {
			transition = append(transition, ms)
		} else {
			steady = append(steady, ms)
		}
	}
	m.SteadyStateN = len(steady)
	m.TransitionRequests = len(transition)
	if len(steady) > 0 {
		m.SteadyStateP99Ms, _ = statistics.Percentile(steady, 99)
	}
	if len(transition) > 0 {
		m.TransitionP99Ms, _ = statistics.Percentile(transition, 99)
	}
	return m
}
