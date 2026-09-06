// Command experiment-012b is the H2 test Stage 11 explicitly could not
// build (docs/StageArtifacts/Stage11.md §12): "Adaptive can become
// trapped when the target judged best changes faster than its
// stale-state mechanism can respond." Made possible by Stage 12 Track C
// (ServiceTimeSchedule) -- a genuinely time-varying target, not a
// failure/recovery proxy.
//
// Design: target A starts fastest (10ms). At t=1s, A degrades sharply
// (10ms -> 150ms) while B simultaneously becomes the fastest (150ms ->
// 10ms) -- an actual swap, not just one target getting worse. At t=2s,
// both revert to their original values. A hot key keeps the SAME target
// under continuous pressure throughout, so a stale decision is actually
// exercised, not just theoretically possible.
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

const outDirName = "experiments/012-model-fidelity/results"

type Result struct {
	Policy             string  `json:"policy"`
	MeanMs             float64 `json:"mean_ms"`
	SwapWindowMeanMs   float64 `json:"swap_window_mean_ms"`    // mean latency during [1s,1.3s), when the swap has happened but a stale policy might not have noticed yet
	SwapWindowShareToA float64 `json:"swap_window_share_to_a"` // fraction of swap-window decisions still going to the now-degraded A
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=========================================================================")
	fmt.Println(" Experiment 012-B: H2 -- Staleness/Oscillation Attack (Track C, first test)")
	fmt.Println("=========================================================================")

	horizon := 3 * time.Second
	seeds := replay.DeriveSeeds(5012)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 100, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}

	targets := []replay.TargetProfile{
		{
			Name: "target-a", ServiceTime: 10 * time.Millisecond,
			ServiceTimeSchedule: []replay.ServiceTimeChange{
				{At: clock.VirtualTime((1 * time.Second).Nanoseconds()), NewServiceTime: 150 * time.Millisecond},
				{At: clock.VirtualTime((2 * time.Second).Nanoseconds()), NewServiceTime: 10 * time.Millisecond},
			},
		},
		{
			Name: "target-b", ServiceTime: 150 * time.Millisecond,
			ServiceTimeSchedule: []replay.ServiceTimeChange{
				{At: clock.VirtualTime((1 * time.Second).Nanoseconds()), NewServiceTime: 10 * time.Millisecond},
				{At: clock.VirtualTime((2 * time.Second).Nanoseconds()), NewServiceTime: 150 * time.Millisecond},
			},
		},
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "012b-h2-staleness", Scenario: scenario}

	shortStale := proxy.DefaultAdaptiveConfig()
	shortStale.StaleAfter = 100 * time.Millisecond
	longStale := proxy.DefaultAdaptiveConfig()
	longStale.StaleAfter = 3 * time.Second // effectively "never treat as stale" within this 3s horizon

	var results []Result
	for i, ps := range []struct {
		name string
		spec replay.PolicySpec
	}{
		{"ewma", replay.EWMAPolicy()},
		{"adaptive-default", replay.AdaptivePolicy()},
		{"adaptive-short-stale-after-100ms", replay.AdaptivePolicyWithConfig(shortStale)},
		{"adaptive-long-stale-after-3s", replay.AdaptivePolicyWithConfig(longStale)},
	} {
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

		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		mean, _ := statistics.Mean(ms)

		var swapMs []float64
		for _, c := range wr.Completions {
			if c.VirtualTimeMs >= 1000 && c.VirtualTimeMs < 1300 {
				swapMs = append(swapMs, float64(c.Latency.Microseconds())/1000.0)
			}
		}
		swapMean := 0.0
		if len(swapMs) > 0 {
			swapMean, _ = statistics.Mean(swapMs)
		}

		toA, total := 0, 0
		for _, r := range wr.Records {
			if r.VirtualTimeMs >= 1000 && r.VirtualTimeMs < 1300 {
				total++
				if r.Target == "target-a" {
					toA++
				}
			}
		}
		shareToA := 0.0
		if total > 0 {
			shareToA = float64(toA) / float64(total)
		}

		results = append(results, Result{Policy: ps.name, MeanMs: mean, SwapWindowMeanMs: swapMean, SwapWindowShareToA: shareToA})
		fmt.Printf("  %-32s overall_mean=%7.2fms  swap-window[1.0-1.3s]_mean=%7.2fms  share_still_to_degraded_A=%.2f%% (of %d decisions)\n",
			ps.name, mean, swapMean, shareToA*100, total)
	}

	fmt.Println("\nH2 hypothesis: Adaptive can lag behind a rapidly-changing 'best target' due to its own staleness mechanism.")
	fmt.Println("Interpretation printed after inspecting the actual numbers above, not assumed in advance.")

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "012-B-h2-staleness-oscillation", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "012B-h2-staleness-oscillation.json"), b, 0644)
	fmt.Println("\nExperiment 012-B complete.")
}
