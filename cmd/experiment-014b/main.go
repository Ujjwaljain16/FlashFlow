// Command experiment-014b is Stage 14 Track B + Program B: does a
// BIMODAL heterogeneity shape (a fast group + a slow group, no gradual
// middle) produce a qualitatively different concentration structure than
// the graduated topology experiment-014a tested? The Stage 13 mechanism
// was heterogeneity -> concentration -> rho -> queueing -> routing
// regime; bimodal topology could produce GROUP concentration (spread
// across several tied-fast targets) rather than SINGLE-target
// concentration, which would change the mechanism materially.
//
// Same fast (15ms) and slow (60ms) absolute service times as
// experiment-014a's own topology extremes, same workload, same capacity
// sweep, same N in {3,5,8} -- roughly 60% fast / 40% slow at each N
// (2+1, 3+2, 5+3) so the fast:slow ratio stays comparable across scale.
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

func bimodalTargets(nFast, nSlow, capacity int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, 0, nFast+nSlow)
	for i := 0; i < nFast; i++ {
		out = append(out, replay.TargetProfile{Name: fmt.Sprintf("fast-%02d", i), ServiceTime: 15 * time.Millisecond, Capacity: capacity})
	}
	for i := 0; i < nSlow; i++ {
		out = append(out, replay.TargetProfile{Name: fmt.Sprintf("slow-%02d", i), ServiceTime: 60 * time.Millisecond, Capacity: capacity})
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
	NFast, NSlow    int
	Capacity        int     `json:"capacity"`
	Policy          string  `json:"policy"`
	MeanMs          float64 `json:"mean_ms"`
	P99Ms           float64 `json:"p99_ms"`
	Top1Share       float64 `json:"top1_share"`
	FastGroupShare  float64 `json:"fast_group_share"` // sum of all fast-* targets' share -- distinguishes SINGLE-target from GROUP concentration
	TargetEntropy   float64 `json:"target_entropy_bits"`
	OfferedRhoMax   float64 `json:"offered_rho_max"`
	AggregateRho    float64 `json:"aggregate_rho"`
	MaxQueueDepth   int     `json:"max_queue_depth"`
	WaitSharePctMax float64 `json:"wait_share_pct_at_max_rho_target"`
}

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

func fastGroupShare(completedByTarget map[string]int, total int) float64 {
	if total == 0 {
		return 0
	}
	sum := 0
	for name, c := range completedByTarget {
		if len(name) >= 4 && name[:4] == "fast" {
			sum += c
		}
	}
	return float64(sum) / float64(total)
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=================================================================")
	fmt.Println(" Experiment 014-B: Bimodal Heterogeneity (Track B + Program B)")
	fmt.Println(" fast group (15ms) + slow group (60ms), no graduated middle")
	fmt.Println("=================================================================")

	var all []Result
	configs := []struct{ nFast, nSlow int }{{2, 1}, {3, 2}, {5, 3}}
	for ci, cfg := range configs {
		n := cfg.nFast + cfg.nSlow
		fmt.Printf("\n########## %d fast + %d slow = %d targets ##########\n", cfg.nFast, cfg.nSlow, n)
		rootSeed := int64(14100 + ci*100)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("%d+%d: generating traffic: %v", cfg.nFast, cfg.nSlow, err)
		}

		for _, capacity := range []int{0, 1, 2, 3} {
			targets := bimodalTargets(cfg.nFast, cfg.nSlow, capacity)
			scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
			v := engine.NewVirtualEngine()
			exp := engine.Experiment{ID: fmt.Sprintf("014b-%df%ds-cap%d", cfg.nFast, cfg.nSlow, capacity), Scenario: scenario}

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
					log.Fatalf("%d+%d/cap=%d/%s: %v", cfg.nFast, cfg.nSlow, capacity, ps.name, err)
				}
				wr := result.WorldResult
				r := Result{NFast: cfg.nFast, NSlow: cfg.nSlow, Capacity: capacity, Policy: ps.name}
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
				r.FastGroupShare = fastGroupShare(wr.CompletedByTarget, completed)
				r.TargetEntropy = entropyBits(wr.CompletedByTarget, completed)
				r.MaxQueueDepth = maxQueueDepth(wr.Trace)

				rhoPerTarget := offeredRhoPerTarget(wr, targets, horizon)
				maxRho, maxTarget := 0.0, ""
				for name, rho := range rhoPerTarget {
					if rho > maxRho {
						maxRho, maxTarget = rho, name
					}
				}
				r.OfferedRhoMax = maxRho

				var totalServiceUnits, totalCapacityUnits float64
				svcByName := map[string]time.Duration{}
				for _, t := range targets {
					svcByName[t.Name] = t.ServiceTime
					c := t.Capacity
					if c <= 0 {
						c = 1
					}
					totalCapacityUnits += float64(c)
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
				fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms  top1=%.3f  fast_group=%.3f  entropy=%.2fbits  rho_max=%.3f(%s)  rho_agg=%.3f  queue_max=%-3d  wait%%=%5.1f\n",
					ps.name, r.MeanMs, r.P99Ms, r.Top1Share, r.FastGroupShare, r.TargetEntropy, r.OfferedRhoMax, maxTarget, r.AggregateRho, r.MaxQueueDepth, r.WaitSharePctMax)
			}
		}
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-B-bimodal-heterogeneity", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014B-bimodal-heterogeneity.json"), b, 0644)
	fmt.Println("\nExperiment 014-B complete.")
}
