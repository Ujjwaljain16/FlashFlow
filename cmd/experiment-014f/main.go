// Command experiment-014f is Stage 14 Section 24 (full policy set) +
// Section 28 (mandatory falsification attempt), combined: does the
// Stage 13 load-blind/load-aware 2-regime split survive beyond 3
// targets, and can it be broken?
//
// Runs all six policies (round-robin, weighted-round-robin,
// least-connections, ewma, p2c-load, adaptive) at below/near/above the
// N=8 near-boundary point established in experiment-014c (Requests=381,
// Capacity=1, same graduated topology). Policies are classified by their
// actual construction (round-robin: no signal at all; weighted-round-
// robin: a STATIC configured weight, frozen at startup; the other four:
// a LIVE, continuously-updated signal) rather than by name, per Stage
// 14's own instruction not to force a classification a policy's
// behavior doesn't support.
//
// This single experiment deliberately covers two sections at once
// because they share the same underlying data: the multi-policy sweep
// IS the falsification attempt (a load-aware policy losing to
// round-robin, or WRR degrading like round-robin under load its static
// weights don't anticipate, would show up directly in this table).
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

// classification is fixed by each policy's actual signal source (see
// internal/replay/policies.go), not chosen to fit the desired result.
var classification = map[string]string{
	"round-robin":          "load-blind",
	"weighted-round-robin": "static-capacity-aware", // configured once, frozen at startup -- not "load-aware" in the live sense
	"least-connections":    "dynamic-load-aware",
	"ewma":                 "dynamic-load-aware",
	"p2c-load":             "dynamic-load-aware",
	"adaptive":             "dynamic-load-aware",
}

func policySpecs() []replay.PolicySpec {
	return []replay.PolicySpec{
		replay.RoundRobinPolicy(),
		replay.WeightedRoundRobinPolicy(),
		replay.LeastConnectionsPolicy(),
		replay.EWMAPolicy(),
		replay.P2CLoadPolicy(),
		replay.AdaptivePolicy(),
	}
}

type Result struct {
	Level           string  `json:"level"`
	Requests        int     `json:"requests"`
	Policy          string  `json:"policy"`
	Classification  string  `json:"classification"`
	MeanMs          float64 `json:"mean_ms"`
	P99Ms           float64 `json:"p99_ms"`
	Top1Share       float64 `json:"top1_share"`
	OfferedRhoMax   float64 `json:"offered_rho_max"`
	WaitSharePctMax float64 `json:"wait_share_pct_at_max_rho_target"`
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

func runLevel(name string, requests int, seed int64) []Result {
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
	exp := engine.Experiment{ID: "014f-" + name, Scenario: scenario}

	var results []Result
	for i, spec := range policySpecs() {
		exp.Policy = spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, spec)
		}
		if err != nil {
			log.Fatalf("%s/%s: %v", name, spec.Name, err)
		}
		wr := result.WorldResult
		r := Result{Level: name, Requests: requests, Policy: spec.Name, Classification: classification[spec.Name]}
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
		maxRho, maxTarget := 0.0, ""
		for tname, v := range rho {
			if v > maxRho {
				maxRho, maxTarget = v, tname
			}
		}
		r.OfferedRhoMax = maxRho
		if maxTarget != "" {
			var svcMs float64
			for _, t := range tgts {
				if t.Name == maxTarget {
					svcMs = float64(t.ServiceTime.Microseconds()) / 1000.0
				}
			}
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
					r.WaitSharePctMax = 100 * wait / meanTgt
				}
			}
		}
		results = append(results, r)
		fmt.Printf("  %-22s [%-22s] mean=%9.2fms  p99=%9.2fms  top1=%.3f  rho_max=%.3f(%s)  wait%%=%5.1f\n",
			r.Policy, r.Classification, r.MeanMs, r.P99Ms, r.Top1Share, r.OfferedRhoMax, maxTarget, r.WaitSharePctMax)
	}
	return results
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=================================================================================")
	fmt.Println(" Experiment 014-F: Full Policy Set at N=8 Boundary (Section 24 + Falsification 28)")
	fmt.Println("=================================================================================")

	// Same graduated N=8 topology as 014c/014d; below/near/above chosen
	// around 014c's own near-boundary point (Requests=381).
	levels := []struct {
		name     string
		requests int
		seed     int64
	}{
		{"below_boundary", 150, 14500},
		{"near_boundary", 381, 14501},
		{"above_boundary", 600, 14502},
	}

	var all []Result
	for _, lv := range levels {
		all = append(all, runLevel(lv.name, lv.requests, lv.seed)...)
	}

	fmt.Println("\n--- Falsification check: did any load-aware policy lose to round-robin? ---")
	byLevel := map[string]map[string]Result{}
	for _, r := range all {
		if byLevel[r.Level] == nil {
			byLevel[r.Level] = map[string]Result{}
		}
		byLevel[r.Level][r.Policy] = r
	}
	falsified := false
	for _, lv := range levels {
		rr := byLevel[lv.name]["round-robin"]
		for _, pname := range []string{"weighted-round-robin", "least-connections", "ewma", "p2c-load", "adaptive"} {
			p := byLevel[lv.name][pname]
			if p.MeanMs > rr.MeanMs {
				fmt.Printf("  FALSIFIER FOUND: at %s, %s (mean=%.2fms) is WORSE than round-robin (mean=%.2fms)\n", lv.name, pname, p.MeanMs, rr.MeanMs)
				falsified = true
			}
		}
	}
	if !falsified {
		fmt.Println("  none found: every load-aware/static-capacity-aware policy beat round-robin at every level tested")
	}

	fmt.Println("\n--- Does WRR (static-capacity-aware) behave like round-robin (load-blind) under overload? ---")
	for _, lv := range levels {
		rr, wrr := byLevel[lv.name]["round-robin"], byLevel[lv.name]["weighted-round-robin"]
		fmt.Printf("  %-16s rr_mean=%9.2fms  wrr_mean=%9.2fms  ratio(wrr/rr)=%.3f\n", lv.name, rr.MeanMs, wrr.MeanMs, wrr.MeanMs/rr.MeanMs)
	}

	fmt.Println("\n--- Do dynamic load-aware policies (LC, EWMA, P2C, Adaptive) agree with each other? ---")
	for _, lv := range levels {
		fmt.Printf("  %-16s", lv.name)
		for _, pname := range []string{"least-connections", "ewma", "p2c-load", "adaptive"} {
			p := byLevel[lv.name][pname]
			fmt.Printf("  %s=%.2fms", pname, p.MeanMs)
		}
		fmt.Println()
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-F-full-policy-set-and-falsification", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014F-full-policy-set-and-falsification.json"), b, 0644)
	fmt.Println("\nExperiment 014-F complete.")
}
