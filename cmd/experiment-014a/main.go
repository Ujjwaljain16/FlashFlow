// Command experiment-014a is Stage 14 Track A + Program A: does the
// Stage 13 capacity-boundary regime survive beyond 3 targets? Builds a
// GRADUATED topology at 3, 5, and 8 targets, always starting at the
// same 15ms fastest-target service time and stepping by 15ms per
// target, with IDENTICAL workload (300 requests, 4s horizon,
// HotColdKeys(0.5)) across all three target counts -- the only thing
// that changes is how many (and how slow) the additional targets are.
//
// This deliberately isolates "more targets, same fastest-target/offered-
// load relationship" from "different topology": since EWMA always
// concentrates onto whichever single target it locks onto first
// (usually the fastest), the offered load reaching THAT target should
// stay roughly constant across target counts if total system offered
// load and hot-key skew are held fixed -- letting this experiment ask
// directly whether the transition capacity/rho for the CONCENTRATED
// target moves as N grows, or whether it's invariant to how many other
// (slower) targets exist alongside it.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/014-scale-topology/results"
const horizon = 4 * time.Second
const requests = 300

// graduatedTargets builds n targets at 15ms, 30ms, 45ms, ... (n*15)ms,
// all sharing the given capacity.
func graduatedTargets(n, capacity int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, n)
	for i := 0; i < n; i++ {
		out[i] = replay.TargetProfile{
			Name:        fmt.Sprintf("edge-%02d", i),
			ServiceTime: time.Duration(15*(i+1)) * time.Millisecond,
			Capacity:    capacity,
		}
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
	TargetCount     int     `json:"target_count"`
	Capacity        int     `json:"capacity"`
	Policy          string  `json:"policy"`
	Completed       int     `json:"completed"`
	Rejected        int     `json:"rejected"`
	MeanMs          float64 `json:"mean_ms"`
	P95Ms           float64 `json:"p95_ms"`
	P99Ms           float64 `json:"p99_ms"`
	Top1Share       float64 `json:"top1_share"`
	Top3Share       float64 `json:"top3_share"` // meaningless (==1.0) for n<=3, informative for n=5,8
	TargetEntropy   float64 `json:"target_entropy_bits"`
	OfferedRhoMax   float64 `json:"offered_rho_max"` // capacity-normalized, dispatch-based (immune to horizon truncation, per Stage 13's own corrected formula)
	AggregateRho    float64 `json:"aggregate_rho"`   // total dispatched work / total system capacity -- DISTINCT from OfferedRhoMax, per Stage 14 Section 13
	MaxQueueDepth   int     `json:"max_queue_depth"`
	WaitSharePctMax float64 `json:"wait_share_pct_at_max_rho_target"`
}

// offeredRhoPerTarget computes capacity-normalized offered rho for every
// target from dispatch records (not completions -- Stage 13's own fix
// for horizon-truncation bias).
func offeredRhoPerTarget(wr *replay.WorldResult, targetsList []replay.TargetProfile, horizon time.Duration) map[string]float64 {
	svc := make(map[string]time.Duration, len(targetsList))
	capacity := make(map[string]int, len(targetsList))
	for _, t := range targetsList {
		svc[t.Name] = t.ServiceTime
		c := t.Capacity
		if c <= 0 {
			c = 1
		}
		capacity[t.Name] = c
	}
	dispatched := make(map[string]int, len(targetsList))
	for _, r := range wr.Records {
		dispatched[r.Target]++
	}
	out := make(map[string]float64, len(targetsList))
	for name, count := range dispatched {
		lambda := float64(count) / horizon.Seconds()
		out[name] = lambda * svc[name].Seconds() / float64(capacity[name])
	}
	return out
}

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

func topKShare(completedByTarget map[string]int, total, k int) float64 {
	counts := make([]int, 0, len(completedByTarget))
	for _, c := range completedByTarget {
		counts = append(counts, c)
	}
	// simple insertion sort descending -- target counts here are always small (<=8)
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

func entropyBits(completedByTarget map[string]int, total int) float64 {
	if total == 0 {
		return 0
	}
	h := 0.0
	for _, c := range completedByTarget {
		if c == 0 {
			continue
		}
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=============================================================================")
	fmt.Println(" Experiment 014-A: Multi-Target Capacity Boundary (Track A + Program A)")
	fmt.Println(" graduated topology, target count in {3,5,8}, SAME fastest-target service time")
	fmt.Println("=============================================================================")

	var all []Result
	for ti, n := range []int{3, 5, 8} {
		fmt.Printf("\n########## Target Count: %d ##########\n", n)
		rootSeed := int64(14000 + ti*100)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("n=%d: generating traffic: %v", n, err)
		}

		for _, capacity := range []int{0, 1, 2, 3} {
			targets := graduatedTargets(n, capacity)
			scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
			v := engine.NewVirtualEngine()
			exp := engine.Experiment{ID: fmt.Sprintf("014a-n%d-cap%d", n, capacity), Scenario: scenario}

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
					log.Fatalf("n=%d/cap=%d/%s: %v", n, capacity, ps.name, err)
				}
				wr := result.WorldResult
				r := Result{TargetCount: n, Capacity: capacity, Policy: ps.name, Completed: len(wr.Completions), Rejected: wr.RejectedCount}
				if len(wr.Completions) > 0 {
					ms := make([]float64, len(wr.Completions))
					for i, c := range wr.Completions {
						ms[i] = float64(c.Latency.Microseconds()) / 1000.0
					}
					r.MeanMs, _ = statistics.Mean(ms)
					r.P95Ms, _ = statistics.Percentile(ms, 95)
					r.P99Ms, _ = statistics.Percentile(ms, 99)
				}
				r.Top1Share = topKShare(wr.CompletedByTarget, r.Completed, 1)
				r.Top3Share = topKShare(wr.CompletedByTarget, r.Completed, 3)
				r.TargetEntropy = entropyBits(wr.CompletedByTarget, r.Completed)
				r.MaxQueueDepth = maxQueueDepth(wr.Trace)

				rhoPerTarget := offeredRhoPerTarget(wr, targets, horizon)
				maxRho, maxTarget := 0.0, ""
				sumRho, totalCapacityUnits := 0.0, 0.0
				for name, rho := range rhoPerTarget {
					if rho > maxRho {
						maxRho, maxTarget = rho, name
					}
					sumRho += rho
				}
				for _, t := range targets {
					c := t.Capacity
					if c <= 0 {
						c = 1
					}
					totalCapacityUnits += float64(c)
				}
				r.OfferedRhoMax = maxRho
				// Aggregate rho: total dispatched work (in units of "one
				// slot's worth of service") over total system capacity --
				// DISTINCT from the max-target rho above (Section 13).
				var totalServiceUnits float64
				svcByName := map[string]time.Duration{}
				for _, t := range targets {
					svcByName[t.Name] = t.ServiceTime
				}
				dispatchedByTarget := map[string]int{}
				for _, rr := range wr.Records {
					dispatchedByTarget[rr.Target]++
				}
				for name, count := range dispatchedByTarget {
					totalServiceUnits += float64(count) * svcByName[name].Seconds()
				}
				r.AggregateRho = totalServiceUnits / horizon.Seconds() / totalCapacityUnits

				if maxTarget != "" {
					svcMs := float64(svcByName[maxTarget].Microseconds()) / 1000.0
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

				all = append(all, r)
				fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms  top1=%.3f  top3=%.3f  entropy=%.2fbits  rho_max=%.3f  rho_agg=%.3f  queue_max=%-3d  wait%%=%5.1f\n",
					r.Policy, r.MeanMs, r.P99Ms, r.Top1Share, r.Top3Share, r.TargetEntropy, r.OfferedRhoMax, r.AggregateRho, r.MaxQueueDepth, r.WaitSharePctMax)
			}
		}
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-A-multi-target-capacity-boundary", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014A-multi-target-capacity-boundary.json"), b, 0644)
	fmt.Println("\nExperiment 014-A complete.")
}
