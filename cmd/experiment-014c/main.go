// Command experiment-014c is Stage 14's central experiment (Track C +
// Program C): does similar normalized offered load (rho at the
// concentrated target) produce similar policy behavior across different
// target counts? Stage 13's strongest evidence came from two
// independent sweep dimensions landing on the SAME rho~0.89-0.97 zone
// at N=3. This tests whether targeting that SAME zone at N=5 and N=8
// (by scaling arrival rate to compensate for each topology's own
// naturally-different concentration percentage, observed directly in
// experiment-014a rather than assumed) reproduces a similar transition,
// or whether experiment-014a/014b's own finding (whole-run rho losing
// predictive power as N grows) means it does not.
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

func graduatedTargets(n int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, n)
	for i := 0; i < n; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: capacity}
	}
	return out
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

type Result struct {
	TargetCount     int     `json:"target_count"`
	Requests        int     `json:"requests"`
	Policy          string  `json:"policy"`
	MeanMs          float64 `json:"mean_ms"`
	P99Ms           float64 `json:"p99_ms"`
	AchievedRhoMax  float64 `json:"achieved_rho_max"`
	Top1Share       float64 `json:"top1_share"`
	WaitSharePctMax float64 `json:"wait_share_pct_at_max_rho_target"`
}

func meanLatency(wr *replay.WorldResult) float64 {
	if len(wr.Completions) == 0 {
		return 0
	}
	ms := make([]float64, len(wr.Completions))
	for i, c := range wr.Completions {
		ms[i] = float64(c.Latency.Microseconds()) / 1000.0
	}
	m, _ := statistics.Mean(ms)
	return m
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

func runCell(n, requestCount int) (Result, Result) {
	targets := graduatedTargets(n)
	rootSeed := int64(14200 + n)
	seeds := replay.DeriveSeeds(rootSeed)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requestCount, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("n=%d: generating traffic: %v", n, err)
	}
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: fmt.Sprintf("014c-n%d-req%d", n, requestCount), Scenario: scenario, Policy: replay.EWMAPolicy()}

	rEwma, err := v.Run(exp)
	if err != nil {
		log.Fatalf("n=%d ewma: %v", n, err)
	}
	rAdaptive, err := v.Replay(exp, replay.AdaptivePolicy())
	if err != nil {
		log.Fatalf("n=%d adaptive: %v", n, err)
	}

	build := func(policy string, wr *replay.WorldResult) Result {
		r := Result{TargetCount: n, Requests: requestCount, Policy: policy, MeanMs: meanLatency(wr)}
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		if len(ms) > 0 {
			r.P99Ms, _ = statistics.Percentile(ms, 99)
		}
		r.Top1Share = topKShare(wr.CompletedByTarget, len(wr.Completions), 1)
		rho := offeredRhoPerTarget(wr, targets, horizon)
		maxRho, maxTarget := 0.0, ""
		for name, v := range rho {
			if v > maxRho {
				maxRho, maxTarget = v, name
			}
		}
		r.AchievedRhoMax = maxRho
		if maxTarget != "" {
			var svcMs float64
			for _, t := range targets {
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
		return r
	}
	return build("ewma", rEwma.WorldResult), build("adaptive", rAdaptive.WorldResult)
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 014-C: Normalized-Rho Cross-Scale Test (Track C + Program C -- THE central test)")
	fmt.Println(" Requests scaled per N to target EWMA's own max-target rho ~ 0.9, per experiment-014a's")
	fmt.Println(" own observed concentration percentage at each N (not assumed, measured).")
	fmt.Println("=====================================================================================")

	// Request counts chosen from experiment-014a's own observed
	// Capacity=1 rho values at Requests=300 (N=3: rho=0.915; N=5:
	// rho=0.802; N=8: rho=0.709) -- scaled up proportionally to target
	// ~0.9 for N=5 and N=8 too. N=3 needs no scaling since it's already
	// almost exactly at the target.
	configs := []struct {
		n        int
		requests int
	}{
		{3, 300},
		{5, 336}, // 300 * (0.9/0.802)
		{8, 381}, // 300 * (0.9/0.709)
	}

	var all []Result
	for _, cfg := range configs {
		fmt.Printf("\n-- N=%d, Requests=%d --\n", cfg.n, cfg.requests)
		ewma, adaptive := runCell(cfg.n, cfg.requests)
		all = append(all, ewma, adaptive)
		fmt.Printf("  ewma      mean=%9.2fms  p99=%9.2fms  achieved_rho_max=%.3f  top1=%.3f  wait%%=%5.1f\n",
			ewma.MeanMs, ewma.P99Ms, ewma.AchievedRhoMax, ewma.Top1Share, ewma.WaitSharePctMax)
		fmt.Printf("  adaptive  mean=%9.2fms  p99=%9.2fms  achieved_rho_max=%.3f  top1=%.3f  wait%%=%5.1f\n",
			adaptive.MeanMs, adaptive.P99Ms, adaptive.AchievedRhoMax, adaptive.Top1Share, adaptive.WaitSharePctMax)
		winner := "EWMA"
		if adaptive.MeanMs < ewma.MeanMs {
			winner = "Adaptive"
		}
		fmt.Printf("  -- winner: %s\n", winner)
	}

	fmt.Println("\n--- Does targeting the SAME rho reproduce a similar transition across N? ---")
	fmt.Println("N   requests  ewma_rho  ewma_mean  adaptive_mean  ratio(ewma/adaptive)  winner")
	for i := 0; i < len(all); i += 2 {
		e, a := all[i], all[i+1]
		ratio := 0.0
		if a.MeanMs > 0 {
			ratio = e.MeanMs / a.MeanMs
		}
		winner := "EWMA"
		if a.MeanMs < e.MeanMs {
			winner = "Adaptive"
		}
		fmt.Printf("%-3d %-9d %-9.3f %-10.2f %-14.2f %-20.2f %s\n", e.TargetCount, e.Requests, e.AchievedRhoMax, e.MeanMs, a.MeanMs, ratio, winner)
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "014-C-normalized-rho-cross-scale", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "014C-normalized-rho-cross-scale.json"), b, 0644)
	fmt.Println("\nExperiment 014-C complete.")
}
