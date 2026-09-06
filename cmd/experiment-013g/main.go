// Command experiment-013g is Stage 13 Program G: a targeted causal
// intervention on internal/proxy.LatencyTracker's EWMA smoothing alpha,
// the suspected (but not yet confirmed) mechanism behind Adaptive's
// residual H2 lag (Stage 12 found StaleAfter had ZERO measurable effect
// across three orders of magnitude -- ruling that mechanism out, not
// confirming an alternative).
//
// LatencyTracker.Observe applies estimate = alpha*sample +
// (1-alpha)*estimate (internal/proxy/latency_tracker.go); both
// EWMAPolicy and AdaptivePolicy hardcode alpha=0.2 (a ~5-request
// averaging window) with no way to configure it via PolicySpec/
// AdaptiveConfig. This experiment builds custom PolicySpecs directly
// from proxy's own exported constructors (NewAdaptiveSelector,
// NewLatencyTracker, NewLoadTracker) -- no internal/ package changes
// needed -- to vary alpha while holding every other signal fixed, using
// the identical H2 swap scenario from experiment-012b.
//
// This is an ablation, not a correlation: if changing alpha changes the
// swap-window lag in the predicted direction (higher alpha = faster
// recognition = less lag), that is real causal evidence. If it doesn't,
// that is reported directly, per this stage's own instruction not to
// conclude smoothing is causal merely because SOMETHING changes.
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

const outDirName = "experiments/013-regime-discovery/results"

// customInstrumentation mirrors internal/replay/policies.go's own
// (unexported) trackerInstrumentation -- duplicated here in ~8 lines
// rather than requiring an internal/ export, since this experiment is
// the only caller that ever needs custom-alpha construction. Critically
// must feed BOTH load and latency, exactly like the original: an
// earlier version of this file only decremented load on completion and
// never called lat.Observe, which would silently leave the tracker at
// its permanent cold-start value regardless of alpha -- caught by
// noticing all four alpha values produced byte-identical results,
// which should have been immediately suspicious rather than accepted.
type customInstrumentation struct {
	load *proxy.LoadTracker
	lat  *proxy.LatencyTracker
}

func (c customInstrumentation) OnDispatch(target string) { c.load.Increment(target) }
func (c customInstrumentation) OnComplete(target string, latency time.Duration) {
	c.load.Decrement(target)
	c.lat.Observe(target, latency)
}

func adaptiveWithAlpha(alpha float64) replay.PolicySpec {
	return replay.PolicySpec{
		Name: fmt.Sprintf("adaptive-alpha%.2f", alpha),
		New: func(clk clock.Clock, seeds replay.SeedTree, targets []replay.TargetProfile, tr replay.Trackers) (proxy.TargetSelector, replay.Instrumentation) {
			load := proxy.NewLoadTracker()
			lat := proxy.NewLatencyTracker(alpha)
			sel := proxy.NewAdaptiveSelector(load, lat, nil, nil, clk, proxy.DefaultAdaptiveConfig())
			return sel, customInstrumentation{load: load, lat: lat}
		},
	}
}

type Result struct {
	Alpha              float64 `json:"alpha"`
	OverallMeanMs      float64 `json:"overall_mean_ms"`
	SwapWindowMeanMs   float64 `json:"swap_window_mean_ms"`
	SwapWindowShareToA float64 `json:"swap_window_share_to_degraded_pct"`
	RequestsToRecover  int     `json:"requests_to_first_correct_post_swap_decision"` // how many hot-key decisions after t=1s until target-b (now fast) is FIRST chosen
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===========================================================================")
	fmt.Println(" Experiment 013-G: H2 Smoothing-Alpha Intervention (causal, not correlational)")
	fmt.Println("===========================================================================")

	horizon := 3 * time.Second
	seeds := replay.DeriveSeeds(5012) // identical to experiment-012b's own H2 scenario
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
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "013g-h2-smoothing", Scenario: scenario}

	var results []Result
	alphas := []float64{0.05, 0.2, 0.5, 0.9}
	for i, alpha := range alphas {
		spec := adaptiveWithAlpha(alpha)
		exp.Policy = spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, spec)
		}
		if err != nil {
			log.Fatalf("alpha=%.2f: %v", alpha, err)
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

		toA, total, recoverAt := 0, 0, -1
		decisionIdx := 0
		for _, r := range wr.Records {
			if r.VirtualTimeMs < 1000 {
				continue
			}
			decisionIdx++
			if r.VirtualTimeMs >= 1000 && r.VirtualTimeMs < 1300 {
				total++
				if r.Target == "target-a" {
					toA++
				}
			}
			if recoverAt == -1 && r.VirtualTimeMs >= 1000 && r.Target == "target-b" {
				recoverAt = decisionIdx
			}
		}
		shareToA := 0.0
		if total > 0 {
			shareToA = 100 * float64(toA) / float64(total)
		}

		r := Result{Alpha: alpha, OverallMeanMs: mean, SwapWindowMeanMs: swapMean, SwapWindowShareToA: shareToA, RequestsToRecover: recoverAt}
		results = append(results, r)
		fmt.Printf("  alpha=%.2f  overall_mean=%7.2fms  swap_window_mean=%7.2fms  swap_window_share_to_degraded=%.1f%%  decisions_to_first_correct_choice=%d\n",
			r.Alpha, r.OverallMeanMs, r.SwapWindowMeanMs, r.SwapWindowShareToA, r.RequestsToRecover)
	}

	fmt.Println("\n--- Is smoothing alpha causal? ---")
	allIdentical := true
	strictlyDecreasing := true
	for i := 1; i < len(results); i++ {
		if results[i].SwapWindowShareToA != results[0].SwapWindowShareToA {
			allIdentical = false
		}
		if results[i].SwapWindowShareToA >= results[i-1].SwapWindowShareToA {
			strictlyDecreasing = false
		}
	}
	switch {
	case allIdentical:
		fmt.Println("All four alpha values produced IDENTICAL swap-window behavior: alpha has NO measurable")
		fmt.Println("effect here either -- the same negative-result pattern Stage 12 found for StaleAfter.")
	case strictlyDecreasing:
		fmt.Println("Swap-window share-to-degraded decreases STRICTLY as alpha increases: real, causal evidence")
		fmt.Println("that smoothing delay contributes to the residual H2 lag.")
	default:
		fmt.Println("Swap-window share-to-degraded changes with alpha but NOT monotonically: smoothing alpha has")
		fmt.Println("SOME effect but does not cleanly, fully explain the residual lag on its own.")
	}

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "013-G-h2-smoothing-alpha-intervention", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013G-h2-smoothing-alpha-intervention.json"), b, 0644)
	fmt.Println("\nExperiment 013-G complete.")
}
