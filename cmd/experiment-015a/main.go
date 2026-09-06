// Command experiment-015a builds Stage 15's canonical scenario (Section
// 8 of the assignment): 5 heterogeneous, finite-capacity targets under a
// FlashCrowd workload with a deliberate peak-then-decay shape, long
// enough to observe the full build -> detect -> respond -> recover
// sequence within one run (Section 9) -- not just a boundary snapshot
// like Stage 14's own experiments, which typically ended still
// overloaded.
//
// Runs all six policies unmodified (no tuning) and, for each, uses
// internal/backlog to reconstruct: the concentration a policy settles
// into, its concentrated target's own queue-depth timeline (peak depth,
// area-under-curve, time-above-threshold), and the congestion ->
// diversion -> drain pipeline (AnalyzeDiversion) -- the raw material for
// Stage 15's mechanism table (Section 35) and the M1-M5 predictor
// comparison that follows in later experiments.
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
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/015-mechanism-identification/results"
const horizon = 8 * time.Second
const capacity = 1
const targetCount = 5

// Congestion/diversion thresholds, fixed BEFORE any result is inspected
// (Stage 15's own anti-fishing rule, Section 30): a target is
// "congested" once its depth exceeds its capacity (ratio>1.0, i.e.
// genuine queueing beyond the single in-service slot); "diversion" is
// recognized once a trailing 20-decision window shows the congested
// target's own share drop below 50%. Robustness to both choices is
// checked separately in experiment-015c, not substituted here after the
// fact.
const congestionRatioThreshold = 1.0
const diversionWindow = 20
const diversionShareThreshold = 0.5

func canonicalTargets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, targetCount)
	for i := 0; i < targetCount; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: capacity}
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
		{"weighted-round-robin", replay.WeightedRoundRobinPolicy()},
		{"least-connections", replay.LeastConnectionsPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"p2c-load", replay.P2CLoadPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

type PolicyResult struct {
	Policy               string  `json:"policy"`
	MeanMs               float64 `json:"mean_ms"`
	P99Ms                float64 `json:"p99_ms"`
	Top1Share            float64 `json:"top1_share"`
	Top3Share            float64 `json:"top3_share"`
	EntropyBits          float64 `json:"entropy_bits"`
	TopByCompletions     string  `json:"top_by_completions"` // M1: the target with the most completed requests
	BottleneckTarget     string  `json:"bottleneck_target"`  // M2/M4: the target with the worst queue outcome (highest peak depth/capacity) -- can DIFFER from TopByCompletions, e.g. for round-robin, which spreads completions evenly but still overloads its slowest target the most
	PeakDepth            int     `json:"peak_depth"`
	PeakDepthAtMs        float64 `json:"peak_depth_at_ms"`
	AreaUnderQueue       float64 `json:"area_under_queue"`
	TimeAboveRho1_0Ms    float64 `json:"time_above_rho_1_0_ms"`
	TimeAboveRho0_9Ms    float64 `json:"time_above_rho_0_9_ms"`
	FractionAboveRho1_0  float64 `json:"fraction_above_rho_1_0"`
	CongestionFound      bool    `json:"congestion_found"`
	CongestionAtMs       float64 `json:"congestion_at_ms"`
	DiversionFound       bool    `json:"diversion_found"`
	DiversionAtMs        float64 `json:"diversion_at_ms"`
	CommittedBacklog     int     `json:"committed_backlog"`
	QueueDrainFound      bool    `json:"queue_drain_found"`
	QueueDrainAtMs       float64 `json:"queue_drain_at_ms"`
	FirstDispatchToTopMs float64 `json:"first_dispatch_to_top_ms"`
	QueueBeginsAtMs      float64 `json:"queue_begins_at_ms"` // first time depth>1 (any real waiting at all)
	QueueBeginsFound     bool    `json:"queue_begins_found"`
}

func peakDepthTime(tl backlog.Timeline) (int, float64) {
	depth, peak, peakAt := 0, 0, 0.0
	for _, e := range tl.Events {
		depth += e.Delta
		if depth > peak {
			peak = depth
			peakAt = e.TimeMs
		}
	}
	return peak, peakAt
}

func queueBeginsAt(tl backlog.Timeline, capacity int) (float64, bool) {
	depth := 0
	for _, e := range tl.Events {
		depth += e.Delta
		if float64(depth) > float64(capacity) {
			return e.TimeMs, true
		}
	}
	return 0, false
}

func firstDispatchTo(records []replay.SelectionRecord, target string) float64 {
	for _, r := range records {
		if r.Target == target {
			return r.VirtualTimeMs
		}
	}
	return -1
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-A: Canonical Scenario -- Backlog Dynamics Across All Six Policies")
	fmt.Println("=====================================================================================")

	seeds := replay.DeriveSeeds(15000)
	arrivals, err := traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300,
		BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}
	scenario := replay.Scenario{Targets: canonicalTargets(), Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "015a-canonical", Scenario: scenario}

	var results []PolicyResult
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
			log.Fatalf("%s: %v", ps.name, err)
		}
		wr := result.WorldResult

		r := PolicyResult{Policy: ps.name}
		if len(wr.Completions) > 0 {
			ms := make([]float64, len(wr.Completions))
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			r.MeanMs, _ = statistics.Mean(ms)
			r.P99Ms, _ = statistics.Percentile(ms, 99)
		}

		conc := backlog.ComputeConcentration(wr.CompletedByTarget)
		r.Top1Share, r.Top3Share, r.EntropyBits = conc.Top1Share, conc.Top3Share, conc.EntropyBits

		topByCompletions, topCount := "", -1
		for name, count := range wr.CompletedByTarget {
			if count > topCount {
				topByCompletions, topCount = name, count
			}
		}
		r.TopByCompletions = topByCompletions

		// The bottleneck target is chosen by WORST QUEUE OUTCOME (highest
		// peak depth), not by completion count -- these can diverge for a
		// policy like round-robin, which spreads completions evenly across
		// targets but still overloads its slowest target the most in
		// queueing terms (Stage 13/14's own established finding).
		bottleneck, bottleneckPeak := "", -1
		for _, t := range canonicalTargets() {
			tl := backlog.BuildTimeline(wr.Records, wr.Completions, t.Name)
			peak, _ := peakDepthTime(tl)
			if peak > bottleneckPeak {
				bottleneck, bottleneckPeak = t.Name, peak
			}
		}
		top := bottleneck
		r.BottleneckTarget = bottleneck

		tl := backlog.BuildTimeline(wr.Records, wr.Completions, top)
		r.PeakDepth, r.PeakDepthAtMs = peakDepthTime(tl)
		r.AreaUnderQueue = tl.AreaUnderCurve(float64(horizon.Milliseconds()))
		r.TimeAboveRho1_0Ms = tl.TimeAboveThreshold(capacity, 1.0, float64(horizon.Milliseconds()))
		r.TimeAboveRho0_9Ms = tl.TimeAboveThreshold(capacity, 0.9, float64(horizon.Milliseconds()))
		r.FractionAboveRho1_0 = tl.FractionAboveThreshold(capacity, 1.0, float64(horizon.Milliseconds()))
		r.FirstDispatchToTopMs = firstDispatchTo(wr.Records, top)
		r.QueueBeginsAtMs, r.QueueBeginsFound = queueBeginsAt(tl, capacity)

		// Anchor congestion onset to the episode containing the PEAK
		// depth, not merely the first episode ever observed: a target can
		// have a small, early, self-resolving congestion blip well before
		// the workload's real peak creates its dominant collapse (found
		// directly while building this scenario -- EWMA's bottleneck
		// target had exactly this two-episode shape), and a first-episode-
		// only analysis would badly understate the backlog that actually
		// explains the run's worst outcome.
		onsetMs, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capacity, congestionRatioThreshold)
		cfg := backlog.CongestionConfig{RatioThreshold: congestionRatioThreshold, DiversionWindow: diversionWindow, DiversionShareThreshold: diversionShareThreshold}
		dr := backlog.AnalyzeDiversion(wr.Records, tl, top, capacity, onsetMs, onsetFound, cfg, float64(horizon.Milliseconds()))
		r.CongestionFound, r.CongestionAtMs = dr.CongestionFound, dr.CongestionAtMs
		r.DiversionFound, r.DiversionAtMs = dr.DiversionFound, dr.DiversionAtMs
		r.CommittedBacklog = dr.CommittedBacklog
		r.QueueDrainFound, r.QueueDrainAtMs = dr.QueueDrainFound, dr.QueueDrainAtMs

		results = append(results, r)
		fmt.Printf("\n-- %s --\n", ps.name)
		fmt.Printf("  mean=%9.2fms  p99=%9.2fms  top1=%.3f  top3=%.3f  entropy=%.2fbits  top_by_completions=%s  bottleneck=%s\n",
			r.MeanMs, r.P99Ms, r.Top1Share, r.Top3Share, r.EntropyBits, r.TopByCompletions, r.BottleneckTarget)
		fmt.Printf("  peak_depth=%d @%.0fms  area_under_queue=%.0f  time_above_rho1.0=%.0fms  time_above_rho0.9=%.0fms\n",
			r.PeakDepth, r.PeakDepthAtMs, r.AreaUnderQueue, r.TimeAboveRho1_0Ms, r.TimeAboveRho0_9Ms)
		fmt.Printf("  t0(first_dispatch_to_top)=%.0fms  t1(queue_begins,found=%v)=%.0fms  t2(congestion,found=%v)=%.0fms\n",
			r.FirstDispatchToTopMs, r.QueueBeginsFound, r.QueueBeginsAtMs, r.CongestionFound, r.CongestionAtMs)
		fmt.Printf("  t4(diversion,found=%v)=%.0fms  committed_backlog=%d  t7(drain,found=%v)=%.0fms\n",
			r.DiversionFound, r.DiversionAtMs, r.CommittedBacklog, r.QueueDrainFound, r.QueueDrainAtMs)
	}

	out := struct {
		Experiment string         `json:"experiment"`
		Timestamp  string         `json:"timestamp"`
		Results    []PolicyResult `json:"results"`
	}{Experiment: "015-A-canonical-scenario-backlog-dynamics", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: results}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015A-canonical-scenario-backlog-dynamics.json"), b, 0644)
	fmt.Println("\nExperiment 015-A complete.")
}
