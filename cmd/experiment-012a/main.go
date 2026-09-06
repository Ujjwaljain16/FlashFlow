// Command experiment-012a re-runs Stage 11 Program A's flagship finding
// (severe heterogeneity, constant workload, no failure -- where EWMA beat
// Adaptive on mean latency by concentrating ~97% of traffic onto the
// fastest target, unpenalized because RunWorld had no queueing model)
// under Stage 12's new finite-capacity contention model.
//
// The scientific question (docs/StageArtifacts/Stage12-Plan.md §7,
// Stage 12 assignment §19): does EWMA still beat Adaptive on mean latency
// when concentrating traffic onto the fastest target now carries an
// actual capacity cost? Capacity=1 per target (each server serves
// exactly one request at a time) is used deliberately as the simplest,
// most conservative, most interpretable contention assumption -- chosen
// BEFORE running this experiment, not tuned afterward to produce a
// particular answer.
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
)

const outDirName = "experiments/012-model-fidelity/results"

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
	Policy      string             `json:"policy"`
	Capacity    int                `json:"capacity"`
	Completed   int                `json:"completed"`
	MeanMs      float64            `json:"mean_ms"`
	P50Ms       float64            `json:"p50_ms"`
	P95Ms       float64            `json:"p95_ms"`
	P99Ms       float64            `json:"p99_ms"`
	MaxShare    float64            `json:"max_share"`
	Utilization map[string]float64 `json:"utilization_rho"`
}

func targets(capacity int) []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=============================================================================")
	fmt.Println(" Experiment 012-A: Program A Flagship Finding Rebuilt Under Contention")
	fmt.Println(" severe heterogeneity, constant workload, no failure -- Capacity=1 per target")
	fmt.Println("=============================================================================")

	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(1001) // identical root seed to Stage 11's own severe/constant/none flagship
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}

	capacityLabels := map[int]string{0: "flat model, matches Stage 11 exactly", 1: "contention-enabled, 1 slot/target", 2: "contention-enabled, 2 slots/target", 3: "contention-enabled, 3 slots/target"}
	var results []Result
	for _, capacity := range []int{0, 1, 2, 3} { // 0 = flat/infinite (Stage 11's own baseline); 1-3 = increasingly forgiving contention, to trace the regime boundary rather than report one point
		fmt.Printf("\n-- Capacity=%d (%s) --\n", capacity, capacityLabels[capacity])
		scenario := replay.Scenario{
			Targets: targets(capacity), Arrivals: arrivals,
			Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
		}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("012a-capacity%d", capacity), Scenario: scenario}

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
				log.Fatalf("capacity=%d, policy=%s: %v", capacity, ps.name, err)
			}
			wr := result.WorldResult
			r := Result{Policy: ps.name, Capacity: capacity, Completed: len(wr.Completions)}
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
			if util, err := attribution.UtilizationFromWorld(*wr, targets(capacity), horizon); err == nil {
				r.Utilization = util
			}
			results = append(results, r)
			fmt.Printf("  %-14s completed=%-4d mean=%7.2fms  p50=%7.2fms  p95=%7.2fms  p99=%7.2fms  max_share=%.3f  rho=%v\n",
				r.Policy, r.Completed, r.MeanMs, r.P50Ms, r.P95Ms, r.P99Ms, r.MaxShare, r.Utilization)
		}
	}

	fmt.Println("\n--- The central Stage 12 question: does the winner change as capacity tightens? ---")
	byCapPolicy := map[string]Result{}
	for _, r := range results {
		byCapPolicy[fmt.Sprintf("%d/%s", r.Capacity, r.Policy)] = r
	}
	for _, capacity := range []int{0, 1, 2, 3} {
		e, a := byCapPolicy[fmt.Sprintf("%d/ewma", capacity)], byCapPolicy[fmt.Sprintf("%d/adaptive", capacity)]
		fmt.Printf("Capacity=%d: EWMA=%7.2fms  Adaptive=%7.2fms  -- %s wins\n", capacity, e.MeanMs, a.MeanMs, winner(e.MeanMs, a.MeanMs))
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "012-A-program-a-under-contention", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "012A-program-a-under-contention.json"), b, 0644)
	fmt.Println("\nExperiment 012-A complete.")
}

func winner(ewmaMs, adaptiveMs float64) string {
	if ewmaMs < adaptiveMs {
		return "EWMA"
	}
	return "Adaptive"
}
