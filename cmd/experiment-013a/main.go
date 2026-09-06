// Command experiment-013a is Stage 13 Program A (+ folded-in Program C):
// does Stage 12's capacity-dependent EWMA/Adaptive reversal generalize
// across topology/service-time configurations, or was it a property of
// one specific severe-heterogeneity scenario?
//
// Design: 3 heterogeneity levels (low/moderate/severe) x a capacity
// sweep {0 (flat baseline), 1, 2, 3, 4, 5} x 3 policies
// (round-robin/ewma/adaptive), holding workload shape, request count,
// and arrival rate fixed across all cells -- the only things that vary
// are heterogeneity level and capacity. Using the SAME capacity integers
// across all three heterogeneity levels is deliberate, not an oversight:
// since each level's fastest target has a different ServiceTime, the
// same Capacity value produces a DIFFERENT rho per level -- this alone
// is a direct, built-in test of whether the transition tracks rho or
// tracks the raw capacity number (Stage 13's central question), without
// needing a separate calibration pass.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/013-regime-discovery/results"

type HeterogeneityLevel struct {
	Name    string
	Targets []replay.TargetProfile // Capacity filled in per-cell
}

func heterogeneityLevels() []HeterogeneityLevel {
	return []HeterogeneityLevel{
		{"low", []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 10 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 15 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 20 * time.Millisecond},
		}},
		{"moderate", []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 10 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 20 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 40 * time.Millisecond},
		}},
		{"severe", []replay.TargetProfile{ // identical to Stage 11/12's own flagship, for continuity
			{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
		}},
	}
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
	Heterogeneity string             `json:"heterogeneity"`
	Capacity      int                `json:"capacity"`
	Policy        string             `json:"policy"`
	Completed     int                `json:"completed"`
	Rejected      int                `json:"rejected"`
	MeanMs        float64            `json:"mean_ms"`
	P50Ms         float64            `json:"p50_ms"`
	P95Ms         float64            `json:"p95_ms"`
	P99Ms         float64            `json:"p99_ms"`
	MaxShare      float64            `json:"max_share"`
	Utilization   map[string]float64 `json:"utilization_rho"`
	MaxRho        float64            `json:"max_rho"` // rho of the most-utilized target -- the "concentrated target" the Stage 13 thesis is about
	MaxQueueDepth int                `json:"max_queue_depth"`
	WaitSharePct  float64            `json:"wait_share_pct"` // fraction of MEAN latency attributable to waiting, at the max-rho target
}

const (
	requests = 300
	horizon  = 4 * time.Second
	baseRate = 75.0
)

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 013-A: Capacity Boundary Generalization Across Heterogeneity Levels")
	fmt.Println(" (Stage 13 Programs A+C -- does Stage 12's reversal generalize, or was it scenario-specific?)")
	fmt.Println("=====================================================================================")

	var all []Result
	for hi, level := range heterogeneityLevels() {
		fmt.Printf("\n########## Heterogeneity: %s (%v) ##########\n", level.Name, serviceTimesOf(level.Targets))
		rootSeed := int64(13000 + hi*100)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, BaseRate: baseRate, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("%s: generating traffic: %v", level.Name, err)
		}

		for _, capacity := range []int{0, 1, 2, 3, 4, 5} {
			targets := withCapacity(level.Targets, capacity)
			scenario := replay.Scenario{
				Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
			}
			v := engine.NewVirtualEngine()
			exp := engine.Experiment{ID: fmt.Sprintf("013a-%s-cap%d", level.Name, capacity), Scenario: scenario}

			fmt.Printf("\n-- capacity=%d --\n", capacity)
			var cellWinner string
			var cellBestMean = 1e18
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
					log.Fatalf("%s/cap%d/%s: %v", level.Name, capacity, ps.name, err)
				}
				wr := result.WorldResult
				r := Result{Heterogeneity: level.Name, Capacity: capacity, Policy: ps.name, Completed: len(wr.Completions), Rejected: wr.RejectedCount}
				if len(wr.Completions) > 0 {
					ms := make([]float64, len(wr.Completions))
					for i, c := range wr.Completions {
						ms[i] = float64(c.Latency.Microseconds()) / 1000.0
					}
					r.MeanMs, _ = statistics.Mean(ms)
					r.P50Ms, _ = statistics.Percentile(ms, 50)
					r.P95Ms, _ = statistics.Percentile(ms, 95)
					r.P99Ms, _ = statistics.Percentile(ms, 99)
					maxCount := 0
					for _, c := range wr.CompletedByTarget {
						if c > maxCount {
							maxCount = c
						}
					}
					r.MaxShare = float64(maxCount) / float64(len(wr.Completions))
				}
				if util, err := attribution.UtilizationFromWorld(*wr, targets, horizon); err == nil {
					r.Utilization = util
					maxTarget, maxRho := "", 0.0
					for t, rho := range util {
						if rho > maxRho {
							maxRho, maxTarget = rho, t
						}
					}
					r.MaxRho = maxRho
					// Wait share at the max-rho target: mean latency there minus its
					// (possibly still-flat) ServiceTime, over mean latency there.
					svcMs := serviceTimeMsOf(targets, maxTarget)
					var tgtLat []float64
					for _, c := range wr.Completions {
						if c.Target == maxTarget {
							tgtLat = append(tgtLat, float64(c.Latency.Microseconds())/1000.0)
						}
					}
					if len(tgtLat) > 0 {
						meanTgt, _ := statistics.Mean(tgtLat)
						wait := meanTgt - svcMs
						if wait < 0 {
							wait = 0
						}
						if meanTgt > 0 {
							r.WaitSharePct = 100 * wait / meanTgt
						}
					}
				}
				r.MaxQueueDepth = maxQueueDepth(wr.Trace)

				all = append(all, r)
				fmt.Printf("  %-14s completed=%-4d mean=%9.2fms  max_share=%.3f  max_rho=%.3f  max_queue=%-3d  wait_share=%5.1f%%\n",
					r.Policy, r.Completed, r.MeanMs, r.MaxShare, r.MaxRho, r.MaxQueueDepth, r.WaitSharePct)
				if r.Policy == "ewma" || r.Policy == "adaptive" {
					if r.MeanMs < cellBestMean {
						cellBestMean, cellWinner = r.MeanMs, r.Policy
					}
				}
			}
			fmt.Printf("  >> winner (ewma vs adaptive): %s\n", cellWinner)
		}
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "013-A-capacity-boundary-generalization", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013A-capacity-boundary-generalization.json"), b, 0644)
	fmt.Println("\nExperiment 013-A complete.")
}

func withCapacity(targets []replay.TargetProfile, capacity int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, len(targets))
	for i, t := range targets {
		t.Capacity = capacity
		out[i] = t
	}
	return out
}

func serviceTimesOf(targets []replay.TargetProfile) []time.Duration {
	out := make([]time.Duration, len(targets))
	for i, t := range targets {
		out[i] = t.ServiceTime
	}
	return out
}

func serviceTimeMsOf(targets []replay.TargetProfile, name string) float64 {
	for _, t := range targets {
		if t.Name == name {
			return float64(t.ServiceTime.Microseconds()) / 1000.0
		}
	}
	return 0
}

// maxQueueDepth scans the trace for "request_queued" events (recorded by
// RunWorld at every enqueue, Stage 12 Track D) and returns the maximum
// observed queue_depth. This IS the true historical maximum, not an
// approximation: queue length only grows at an enqueue (recorded) and
// only shrinks at a dequeue (not separately recorded), so the peak value
// recorded at any one enqueue can never be exceeded between enqueues.
func maxQueueDepth(trace []vtime.TraceEvent) int {
	max := 0
	for _, e := range trace {
		if e.Type != "request_queued" {
			continue
		}
		if depth, ok := e.Fields["queue_depth"].(int); ok && depth > max {
			max = depth
		}
	}
	return max
}
