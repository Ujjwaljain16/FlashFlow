// Command experiment-016-flagship is Stage 16's flagship demonstration.
// It deliberately introduces NO new topology model: this is
// experiment-015a's own canonical scenario (5 heterogeneous targets,
// 15-75ms, Capacity=1, a FlashCrowd workload peaking at t=2.5s, 8s
// horizon) run across three independent seeds to confirm the
// conclusion is not an artifact of one arrival realization, plus an
// ASCII queue-depth timeline for one representative seed showing the
// mechanism directly (concentration -> queue formation -> committed
// work -> policy divergence -> tail outcome), not just a latency
// leaderboard.
//
// This experiment was chosen as the flagship, not because it produces
// the largest-looking percentage difference, but because it is the one
// scenario in this project that shows all six policies' full mechanism
// classes side by side, including the negative result Stage 15 found:
// Adaptive's own P99 was the worst of the six policies tested here.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/016-final-synthesis/results"
const horizon = 8 * time.Second
const capacity = 1
const targetCount = 5
const congestionRatioThreshold = 1.0
const diversionWindow = 20

// diversionShareThreshold is scaled to 1.5x each target's own "fair
// share" (1/targetCount), not a fixed 0.5 -- a genuine reproducibility
// finding from this stage's own multi-seed audit, not a pre-existing
// design choice. Stage 14/15's own scenarios were almost all 3-target
// (fair share=1/3, so 1.5x fair share is exactly 0.5, the fixed value
// they always used) -- reusing that same fixed 0.5 unmodified on this
// 5-target canonical scenario (fair share=1/5=0.20) meant "diversion"
// was recognized the moment a policy's share merely dipped BELOW HALF,
// which can happen from ordinary noise while the policy is still
// sending 2-2.5x its fair share to an overloaded target. Confirmed by a
// direct comparison across three independent seeds: at the fixed 0.5
// threshold, diversion was detected within ~40-80ms of congestion onset
// every time (essentially immediately), reporting committed backlogs of
// only 9-10 for Adaptive despite that target's peak depth reaching
// 89-114 and remaining over capacity 60-70% of the entire run; at 1.5x
// fair share (0.30 here), diversion was detected 600-800ms later, and
// committed backlog rose to 93-127 -- consistent with the severity
// every OTHER metric (peak depth, fraction-above-capacity) already
// showed. This does not retroactively change Stage 15's own N=5
// canonical-scenario numbers (preserved as historical record, per this
// project's own documentation discipline), but the flagship
// demonstration uses the corrected, target-count-scaled threshold so
// its own headline committed-backlog numbers are defensible.
var diversionShareThreshold = 1.5 / float64(targetCount)

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

type Result struct {
	Seed             int64   `json:"seed"`
	Policy           string  `json:"policy"`
	MeanMs           float64 `json:"mean_ms"`
	P99Ms            float64 `json:"p99_ms"`
	Bottleneck       string  `json:"bottleneck"`
	PeakDepth        int     `json:"peak_depth"`
	DiversionFound   bool    `json:"diversion_found"` // false means committed_backlog/drain_found below are N/A, not "zero"/"never" -- e.g. a static policy like WRR that never materially changes its own share never triggers a "diversion" event to measure FROM in the first place
	CommittedBacklog int     `json:"committed_backlog"`
	FractionAboveCap float64 `json:"fraction_above_capacity"`
	DrainFound       bool    `json:"drain_found"`
}

func analyze(seed int64, policy string, wr *replay.WorldResult, targets []replay.TargetProfile) Result {
	r := Result{Seed: seed, Policy: policy}
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
	r.FractionAboveCap = tl.FractionAboveThreshold(capacity, congestionRatioThreshold, float64(horizon.Milliseconds()))
	onset, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capacity, congestionRatioThreshold)
	cfg := backlog.CongestionConfig{RatioThreshold: congestionRatioThreshold, DiversionWindow: diversionWindow, DiversionShareThreshold: diversionShareThreshold}
	dr := backlog.AnalyzeDiversion(wr.Records, tl, bottleneck, capacity, onset, onsetFound, cfg, float64(horizon.Milliseconds()))
	r.DiversionFound, r.CommittedBacklog, r.DrainFound = dr.DiversionFound, dr.CommittedBacklog, dr.QueueDrainFound
	return r
}

// sparkline renders a target's queue-depth timeline as a coarse ASCII
// bar chart, one character per bucket, scaled to the GLOBAL max depth
// across all rendered targets so bar heights are comparable to each
// other, not just internally consistent.
var sparkChars = []rune(" ▁▂▃▄▅▆▇█")

func sparkline(tl backlog.Timeline, horizonMs float64, buckets int, globalMax int) string {
	var b strings.Builder
	step := horizonMs / float64(buckets)
	for i := 0; i < buckets; i++ {
		t := (float64(i) + 0.5) * step
		depth := tl.DepthAt(t)
		idx := 0
		if globalMax > 0 {
			idx = depth * (len(sparkChars) - 1) / globalMax
			if idx >= len(sparkChars) {
				idx = len(sparkChars) - 1
			}
		}
		b.WriteRune(sparkChars[idx])
	}
	return b.String()
}

func runSeed(seed int64, targets []replay.TargetProfile, printTimeline bool) []Result {
	seeds := replay.DeriveSeeds(seed)
	arrivals, err := traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300,
		BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond,
		KeyFunc: traffic.HotColdKeys(0.5), JitterFraction: 0.3,
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("seed %d: generating traffic: %v", seed, err)
	}
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: fmt.Sprintf("016-flagship-seed%d", seed), Scenario: scenario}

	var results []Result
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
			log.Fatalf("seed %d/%s: %v", seed, ps.name, err)
		}
		r := analyze(seed, ps.name, result.WorldResult, targets)
		results = append(results, r)
		backlogStr, drainStr := "N/A (never diverted)", "N/A (never diverted)"
		if r.DiversionFound {
			backlogStr = fmt.Sprintf("%d", r.CommittedBacklog)
			drainStr = fmt.Sprintf("%v", r.DrainFound)
		}
		fmt.Printf("  %-22s mean=%9.2fms  p99=%9.2fms  bottleneck=%-9s peak=%3d  committed_backlog=%-18s frac_above_cap=%.3f  drains=%s\n",
			r.Policy, r.MeanMs, r.P99Ms, r.Bottleneck, r.PeakDepth, backlogStr, r.FractionAboveCap, drainStr)

		if printTimeline {
			tl := backlog.BuildTimeline(result.WorldResult.Records, result.WorldResult.Completions, r.Bottleneck)
			globalMax := r.PeakDepth
			if globalMax < 1 {
				globalMax = 1
			}
			fmt.Printf("    %-22s %s  (target=%s, 40 buckets over %v, darker=deeper queue)\n",
				"queue depth:", sparkline(tl, float64(horizon.Milliseconds()), 40, globalMax), r.Bottleneck, horizon)
		}
	}
	return results
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 016-Flagship: Concentration -> Committed Backlog -> Tail Collapse")
	fmt.Println(" (identical scenario to experiment-015a; 3 independent seeds + queue-timeline view)")
	fmt.Println("=====================================================================================")

	targets := canonicalTargets()
	seeds := []int64{16000, 16001, 16002}
	var all []Result
	for i, seed := range seeds {
		fmt.Printf("\n--- Seed %s (%d) ---\n", []string{"A", "B", "C"}[i], seed)
		results := runSeed(seed, targets, i == 0) // timeline only for seed A, to keep output readable
		all = append(all, results...)
	}

	fmt.Println("\n--- Does the conclusion change across seeds? ---")
	fmt.Println("policy                  seedA_mean  seedB_mean  seedC_mean  seedA_backlog  seedB_backlog  seedC_backlog")
	byPolicy := map[string][]Result{}
	for _, r := range all {
		byPolicy[r.Policy] = append(byPolicy[r.Policy], r)
	}
	backlogCell := func(r Result) string {
		if !r.DiversionFound {
			return "N/A"
		}
		return fmt.Sprintf("%d", r.CommittedBacklog)
	}
	for _, ps := range policySpecs() {
		rs := byPolicy[ps.name]
		if len(rs) != 3 {
			continue
		}
		fmt.Printf("%-22s %10.2f %11.2f %11.2f %14s %14s %14s\n",
			ps.name, rs[0].MeanMs, rs[1].MeanMs, rs[2].MeanMs, backlogCell(rs[0]), backlogCell(rs[1]), backlogCell(rs[2]))
	}
	fmt.Println("(backlog=N/A means the policy never registered a material diversion event to measure FROM, e.g. a")
	fmt.Println(" static policy like weighted-round-robin whose own share never crosses the diversion threshold at all --")
	fmt.Println(" not that its committed backlog is genuinely zero.)")

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Seeds      []int64  `json:"seeds"`
		Results    []Result `json:"results"`
	}{Experiment: "016-flagship-concentration-to-collapse", Timestamp: time.Now().UTC().Format(time.RFC3339), Seeds: seeds, Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "016-flagship-results.json"), b, 0644)
	fmt.Println("\nExperiment 016-Flagship complete.")
}
