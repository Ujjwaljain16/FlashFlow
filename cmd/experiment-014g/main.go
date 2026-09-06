// Command experiment-014g is Stage 14 Section 22 (Program G): does
// smoothing alpha affect the MAIN capacity-boundary reversal, not just
// Stage 13's H2 sustained-lag scenario (experiment-013g)?
//
// experiment-014f just found EWMA losing outright to round-robin at the
// N=8 near_boundary and above_boundary cells (Requests=381/600,
// Capacity=1) -- exactly because EWMA locks onto one target via a
// smoothed latency signal and never un-locks once that target starts
// queueing. If smoothing lag is the mechanism, a FASTER-reacting EWMA
// (higher alpha) should recognize the queueing target sooner and recover
// some of that loss; a slower one (lower alpha) should make it worse.
// This is a direct test of that hypothesis at the exact cell where the
// falsifier was found, holding topology/capacity/workload/seed fixed and
// varying ONLY alpha -- the identical custom-PolicySpec pattern
// experiment-013g used for H2, applied here to the MAIN boundary.
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
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/014-scale-topology/results"
const horizon = 4 * time.Second
const capacity = 1
const targetCount = 8

// customInstrumentation mirrors internal/replay/policies.go's own
// (unexported) trackerInstrumentation, duplicated here exactly as
// experiment-013g did, since custom-alpha construction needs direct
// access to proxy.NewLatencyTracker(alpha) rather than the fixed
// alpha=0.2 baked into replay.EWMAPolicy().
type customInstrumentation struct {
	lat *proxy.LatencyTracker
}

func (c customInstrumentation) OnDispatch(target string) {}
func (c customInstrumentation) OnComplete(target string, latency time.Duration) {
	c.lat.Observe(target, latency)
}

func ewmaWithAlpha(alpha float64) replay.PolicySpec {
	return replay.PolicySpec{
		Name: fmt.Sprintf("ewma-alpha%.2f", alpha),
		New: func(clk clock.Clock, seeds replay.SeedTree, targets []replay.TargetProfile, tr replay.Trackers) (proxy.TargetSelector, replay.Instrumentation) {
			lat := proxy.NewLatencyTracker(alpha)
			return proxy.NewEWMASelector(lat), customInstrumentation{lat: lat}
		},
	}
}

func targets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, targetCount)
	for i := 0; i < targetCount; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: capacity}
	}
	return out
}

func offeredRhoPerTarget(wr *replay.WorldResult, targetsList []replay.TargetProfile, horizon time.Duration) map[string]float64 {
	svc := make(map[string]time.Duration, len(targetsList))
	capOf := make(map[string]int, len(targetsList))
	for _, t := range targetsList {
		svc[t.Name] = t.ServiceTime
		c := t.Capacity
		if c <= 0 {
			c = 1
		}
		capOf[t.Name] = c
	}
	dispatched := make(map[string]int, len(targetsList))
	for _, r := range wr.Records {
		dispatched[r.Target]++
	}
	out := make(map[string]float64, len(targetsList))
	for name, count := range dispatched {
		lambda := float64(count) / horizon.Seconds()
		out[name] = lambda * svc[name].Seconds() / float64(capOf[name])
	}
	return out
}

func topKShare(completedByTarget map[string]int, total, k int) float64 {
	counts := make([]int, 0, len(completedByTarget))
	for _, c := range completedByTarget {
		counts = append(counts, c)
	}
	for i := 1; i < len(counts); i++ {
		for j := i; j > 0 && counts[j-1] < counts[j]; j-- {
			counts[j-1], counts[j] = counts[j], counts[j-1]
		}
	}
	sum := 0
	for i := 0; i < k && i < len(counts); i++ {
		sum += counts[i]
	}
	if total == 0 {
		return 0
	}
	return float64(sum) / float64(total)
}

type Result struct {
	Level         string  `json:"level"`
	Requests      int     `json:"requests"`
	Alpha         float64 `json:"alpha"`
	MeanMs        float64 `json:"mean_ms"`
	P99Ms         float64 `json:"p99_ms"`
	Top1Share     float64 `json:"top1_share"`
	OfferedRhoMax float64 `json:"offered_rho_max"`
	BeatsRR       bool    `json:"beats_round_robin"`
}

func roundRobinBaseline(scenario replay.Scenario, v engine.VirtualEngine, id string) float64 {
	exp := engine.Experiment{ID: id + "-rr", Scenario: scenario, Policy: replay.RoundRobinPolicy()}
	result, err := v.Run(exp)
	if err != nil {
		log.Fatalf("round-robin baseline: %v", err)
	}
	ms := make([]float64, len(result.WorldResult.Completions))
	for i, c := range result.WorldResult.Completions {
		ms[i] = float64(c.Latency.Microseconds()) / 1000.0
	}
	mean, _ := statistics.Mean(ms)
	return mean
}

func runLevel(name string, requests int, seed int64, alphas []float64) []Result {
	fmt.Printf("\n-- %s (Requests=%d) --\n", name, requests)
	tgts := targets()
	seeds := replay.DeriveSeeds(seed)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("%s: generating traffic: %v", name, err)
	}
	scenario := replay.Scenario{Targets: tgts, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()

	rrMean := roundRobinBaseline(scenario, v, "014g-"+name)
	fmt.Printf("  round-robin baseline mean=%.2fms\n", rrMean)

	var results []Result
	for i, alpha := range alphas {
		spec := ewmaWithAlpha(alpha)
		exp := engine.Experiment{ID: fmt.Sprintf("014g-%s-alpha%.2f", name, alpha), Scenario: scenario, Policy: spec}
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, spec)
		}
		if err != nil {
			log.Fatalf("%s/alpha=%.2f: %v", name, alpha, err)
		}
		wr := result.WorldResult
		r := Result{Level: name, Requests: requests, Alpha: alpha}
		completed := len(wr.Completions)
		if completed > 0 {
			ms := make([]float64, completed)
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			r.MeanMs, _ = statistics.Mean(ms)
			r.P99Ms, _ = statistics.Percentile(ms, 99)
		}
		r.Top1Share = topKShare(wr.CompletedByTarget, completed, 1)
		rho := offeredRhoPerTarget(wr, tgts, horizon)
		for _, v := range rho {
			if v > r.OfferedRhoMax {
				r.OfferedRhoMax = v
			}
		}
		r.BeatsRR = r.MeanMs < rrMean
		results = append(results, r)
		fmt.Printf("  alpha=%.2f  mean=%9.2fms  p99=%9.2fms  top1=%.3f  rho_max=%.3f  beats_rr=%v\n",
			r.Alpha, r.MeanMs, r.P99Ms, r.Top1Share, r.OfferedRhoMax, r.BeatsRR)
	}
	return results
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 014-G: Smoothing Alpha on the MAIN Capacity Boundary (Section 22)")
	fmt.Println(" Same N=8 near/above-boundary cells where 014f found EWMA losing to round-robin")
	fmt.Println("=====================================================================================")

	alphas := []float64{0.05, 0.1, 0.2, 0.4, 0.8}
	var all []Result
	all = append(all, runLevel("near_boundary", 381, 14501, alphas)...)
	all = append(all, runLevel("above_boundary", 600, 14502, alphas)...)

	fmt.Println("\n--- Does higher alpha (faster reaction) recover EWMA's loss to round-robin? ---")
	byLevel := map[string][]Result{}
	for _, r := range all {
		byLevel[r.Level] = append(byLevel[r.Level], r)
	}
	for _, level := range []string{"near_boundary", "above_boundary"} {
		rs := byLevel[level]
		anyBeats := false
		monotonicImprovement := true
		for i, r := range rs {
			if r.BeatsRR {
				anyBeats = true
			}
			if i > 0 && r.MeanMs > rs[i-1].MeanMs {
				monotonicImprovement = false
			}
		}
		fmt.Printf("  %-16s any_alpha_beats_rr=%v  mean_ms_monotonically_improves_with_alpha=%v\n", level, anyBeats, monotonicImprovement)
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-G-alpha-on-main-boundary", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014G-alpha-on-main-boundary.json"), b, 0644)
	fmt.Println("\nExperiment 014-G complete.")
}
