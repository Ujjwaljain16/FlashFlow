// Command experiment-015c is Stage 15 Section 20: Adaptive's own
// canonical-scenario result was itself surprising (experiment-015a found
// Adaptive committing 86 requests to its SLOWEST target, edge-04, which
// never drains within the 8s horizon -- the second-worst outcome of all
// six policies, undermining any assumption that Adaptive is simply
// "safe"). This experiment determines WHICH of Adaptive's signal
// components is responsible, via controlled ablation (default, cache=0,
// load=0, latency=0 -- the assignment's own minimum list). Cost=0 is not
// tested: this project's own replay.AdaptivePolicyWithConfig always
// calls proxy.NewAdaptiveSelector with nil capacity/cost TargetWeights
// (confirmed by reading internal/replay/policies.go directly), so the
// Cost signal is structurally always-neutral already -- ablating it
// further would be a no-op, not a real experiment.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/015-mechanism-identification/results"
const congestionRatioThreshold = 1.0
const diversionWindow = 20
const diversionShareThreshold = 0.5

func canonicalTargets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, 5)
	for i := 0; i < 5; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: 1}
	}
	return out
}

type AblationResult struct {
	Config           string  `json:"config"`
	MeanMs           float64 `json:"mean_ms"`
	P99Ms            float64 `json:"p99_ms"`
	Bottleneck       string  `json:"bottleneck"`
	PeakDepth        int     `json:"peak_depth"`
	CongestionFound  bool    `json:"congestion_found"`
	CommittedBacklog int     `json:"committed_backlog"`
	DrainFound       bool    `json:"drain_found"`
	DrainAtMs        float64 `json:"drain_at_ms"`
}

func analyze(wr *replay.WorldResult, targets []replay.TargetProfile, capacity int, horizonMs float64) AblationResult {
	var r AblationResult
	if len(wr.Completions) > 0 {
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		r.MeanMs, _ = statistics.Mean(ms)
		r.P99Ms, _ = statistics.Percentile(ms, 99)
	}
	bottleneck, bottleneckPeak := "", -1
	for _, t := range targets {
		tl := backlog.BuildTimeline(wr.Records, wr.Completions, t.Name)
		p := tl.PeakDepth()
		if p > bottleneckPeak {
			bottleneck, bottleneckPeak = t.Name, p
		}
	}
	r.Bottleneck, r.PeakDepth = bottleneck, bottleneckPeak
	tl := backlog.BuildTimeline(wr.Records, wr.Completions, bottleneck)
	onset, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capacity, congestionRatioThreshold)
	cfg := backlog.CongestionConfig{RatioThreshold: congestionRatioThreshold, DiversionWindow: diversionWindow, DiversionShareThreshold: diversionShareThreshold}
	dr := backlog.AnalyzeDiversion(wr.Records, tl, bottleneck, capacity, onset, onsetFound, cfg, horizonMs)
	r.CongestionFound, r.CommittedBacklog, r.DrainFound, r.DrainAtMs = dr.CongestionFound, dr.CommittedBacklog, dr.QueueDrainFound, dr.QueueDrainAtMs
	return r
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-C: Adaptive Signal Ablation on the Canonical Scenario (Section 20)")
	fmt.Println("=====================================================================================")

	horizon := 8 * time.Second
	targets := canonicalTargets()
	seeds := replay.DeriveSeeds(15000) // identical to 015a's own canonical run
	arrivals, err := traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300,
		BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()

	def := proxy.DefaultAdaptiveConfig()
	configs := []struct {
		name string
		cfg  proxy.AdaptiveConfig
	}{
		{"default", def},
		{"cache=0", proxy.AdaptiveConfig{Weights: proxy.AdaptiveWeights{Load: def.Weights.Load, Latency: def.Weights.Latency, Cache: 0, Cost: def.Weights.Cost}, ReferenceLatency: def.ReferenceLatency, StaleAfter: def.StaleAfter}},
		{"load=0", proxy.AdaptiveConfig{Weights: proxy.AdaptiveWeights{Load: 0, Latency: def.Weights.Latency, Cache: def.Weights.Cache, Cost: def.Weights.Cost}, ReferenceLatency: def.ReferenceLatency, StaleAfter: def.StaleAfter}},
		{"latency=0", proxy.AdaptiveConfig{Weights: proxy.AdaptiveWeights{Load: def.Weights.Load, Latency: 0, Cache: def.Weights.Cache, Cost: def.Weights.Cost}, ReferenceLatency: def.ReferenceLatency, StaleAfter: def.StaleAfter}},
	}

	var results []AblationResult
	for i, c := range configs {
		spec := replay.AdaptivePolicyWithConfig(c.cfg)
		exp := engine.Experiment{ID: "015c-" + c.name, Scenario: scenario, Policy: spec}
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, spec)
		}
		if err != nil {
			log.Fatalf("%s: %v", c.name, err)
		}
		r := analyze(result.WorldResult, targets, 1, float64(horizon.Milliseconds()))
		r.Config = c.name
		results = append(results, r)
		fmt.Printf("  %-10s mean=%9.2fms  p99=%9.2fms  bottleneck=%s  peak_depth=%d  congestion=%v  committed_backlog=%d  drain=%v(@%.0fms)\n",
			r.Config, r.MeanMs, r.P99Ms, r.Bottleneck, r.PeakDepth, r.CongestionFound, r.CommittedBacklog, r.DrainFound, r.DrainAtMs)
	}

	fmt.Println("\n--- Which signal removal changes the outcome most from default? ---")
	base := results[0]
	for _, r := range results[1:] {
		fmt.Printf("  %-10s delta_committed_backlog=%+d  delta_peak_depth=%+d  delta_mean_ms=%+.2f\n",
			r.Config, r.CommittedBacklog-base.CommittedBacklog, r.PeakDepth-base.PeakDepth, r.MeanMs-base.MeanMs)
	}

	out := struct {
		Experiment string           `json:"experiment"`
		Timestamp  string           `json:"timestamp"`
		Results    []AblationResult `json:"results"`
	}{Experiment: "015-C-adaptive-signal-ablation", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015C-adaptive-signal-ablation.json"), b, 0644)
	fmt.Println("\nExperiment 015-C complete.")
}
