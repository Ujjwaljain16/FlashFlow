// Command experiment-015b runs Stage 15's falsification program
// (Sections 11-17, 29) against the emerging "concentration-proneness /
// committed-backlog" mechanism. Four targeted attacks; F2 and F5 are
// deliberately not re-run here because experiment-015a's own canonical-
// scenario data already provides direct, sufficient evidence for them
// (round-robin: LOW completion-count concentration, top1=0.212, yet its
// slowest target never drains -- F2; least-connections vs EWMA on the
// IDENTICAL scenario: earlier diversion (2416ms vs 2688ms) tracks with
// dramatically lower committed backlog (6 vs 97) and a much earlier
// drain (4356ms vs 6850ms) -- F5), per this stage's own instruction not
// to run expensive replication on every exploratory point.
//
// F1 -- high concentration, low consequence: does concentration alone
// (M1) predict collapse, or does it require genuine capacity pressure?
// F3 -- similar physical pressure, different committed backlog: an
// alpha ablation on EWMA (fast vs. slow reacting) at the SAME topology/
// workload/seed, isolating reaction speed from everything else.
// F4 -- does the committed-backlog mechanism survive a concentration
// SHAPE change (bimodal group lock-in vs. graduated single-target
// lock-in), the way Stage 14's own 014b found the RHO mechanism did?
// F6 -- P2C vs. EWMA across independent seeds: is P2C's canonical-
// scenario advantage a structural difference (sampling never fully
// commits) or a lucky seed?
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

// --- shared helpers (mirroring experiment-015a's own, kept local per
// this project's established per-experiment duplication precedent for
// small glue code; the reusable ANALYSIS itself lives in internal/backlog) ---

func bottleneckAndMetrics(wr *replay.WorldResult, targets []replay.TargetProfile, capacity int, horizonMs float64) (target string, peak int, backlogCount int, congestionFound, drainFound bool, drainAtMs float64, meanMs, p99Ms float64) {
	bottleneck, bottleneckPeak := "", -1
	for _, t := range targets {
		tl := backlog.BuildTimeline(wr.Records, wr.Completions, t.Name)
		p := tl.PeakDepth()
		if p > bottleneckPeak {
			bottleneck, bottleneckPeak = t.Name, p
		}
	}
	tl := backlog.BuildTimeline(wr.Records, wr.Completions, bottleneck)
	onset, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capacity, congestionRatioThreshold)
	cfg := backlog.CongestionConfig{RatioThreshold: congestionRatioThreshold, DiversionWindow: diversionWindow, DiversionShareThreshold: diversionShareThreshold}
	dr := backlog.AnalyzeDiversion(wr.Records, tl, bottleneck, capacity, onset, onsetFound, cfg, horizonMs)

	if len(wr.Completions) > 0 {
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		meanMs, _ = statistics.Mean(ms)
		p99Ms, _ = statistics.Percentile(ms, 99)
	}
	return bottleneck, bottleneckPeak, dr.CommittedBacklog, dr.CongestionFound, dr.QueueDrainFound, dr.QueueDrainAtMs, meanMs, p99Ms
}

// --- F1: high concentration, low consequence ---

func runF1() {
	fmt.Println("\n=== F1: High Concentration, Low Consequence ===")
	fmt.Println("(Adaptive on a 3-target topology at load far under capacity: full concentration should be safe)")
	targets := []replay.TargetProfile{
		{Name: "edge-00", ServiceTime: 15 * time.Millisecond, Capacity: 2},
		{Name: "edge-01", ServiceTime: 30 * time.Millisecond, Capacity: 2},
		{Name: "edge-02", ServiceTime: 60 * time.Millisecond, Capacity: 2},
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(15100)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 50, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("F1: generating traffic: %v", err)
	}
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "015b-f1", Scenario: scenario, Policy: replay.AdaptivePolicy()}
	result, err := v.Run(exp)
	if err != nil {
		log.Fatalf("F1: %v", err)
	}
	wr := result.WorldResult
	conc := backlog.ComputeConcentration(wr.CompletedByTarget)
	target, peak, committed, congestionFound, drainFound, drainAt, mean, p99 := bottleneckAndMetrics(wr, targets, 2, float64(horizon.Milliseconds()))
	fmt.Printf("  top1_share=%.3f (M1: HIGH concentration)  bottleneck=%s peak_depth=%d  congestion_found=%v  committed_backlog=%d\n",
		conc.Top1Share, target, peak, congestionFound, committed)
	fmt.Printf("  mean=%.2fms  p99=%.2fms  drain_found=%v(@%.0fms)\n", mean, p99, drainFound, drainAt)
	if conc.Top1Share > 0.7 && !congestionFound && committed == 0 {
		fmt.Println("  RESULT: F1 CONFIRMED -- high concentration (M1) occurred WITHOUT any congestion or committed backlog.")
		fmt.Println("  Concentration alone does NOT predict collapse; capacity pressure is a necessary additional ingredient.")
	} else {
		fmt.Println("  RESULT: F1 NOT confirmed as designed -- either concentration wasn't high, or congestion occurred anyway. Investigate.")
	}
}

// --- F3: alpha ablation on EWMA (reaction speed vs. committed backlog) ---

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

func canonicalTargets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, 5)
	for i := 0; i < 5; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: 1}
	}
	return out
}

// canonicalArrivals reuses the canonical scenario's own workload shape.
// jitterFraction is 0 for single-seed comparisons (F3/F4 -- exact
// determinism is what isolates the one variable each of those tests
// changes); F6's independent-seed replication passes a nonzero jitter,
// since traffic.Generate's FlashCrowd/Constant/etc. positions are
// otherwise a deterministic function of pattern+params alone (inverse-
// CDF sampling, not per-arrival randomness) -- without jitter, "seed"
// would only vary a policy's OWN internal randomness (e.g. P2C's pair
// sampling), not the arrival stream itself, which would silently
// understate what "independent seeds" is supposed to test.
func canonicalArrivals(seeds replay.SeedTree, horizon time.Duration, jitterFraction float64) []replay.Arrival {
	arrivals, err := traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300,
		BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5),
		JitterFraction: jitterFraction,
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating canonical traffic: %v", err)
	}
	return arrivals
}

func runF3() {
	fmt.Println("\n=== F3: Alpha Ablation -- Does Reaction Speed Alone Explain Committed Backlog? ===")
	fmt.Println("(Same topology/workload/seed as the canonical scenario; only EWMA's smoothing alpha varies)")
	horizon := 8 * time.Second
	targets := canonicalTargets()
	seeds := replay.DeriveSeeds(15000) // IDENTICAL root seed to 015a's canonical run, deliberately -- isolates alpha as the only variable
	arrivals := canonicalArrivals(seeds, horizon, 0)
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
	v := engine.NewVirtualEngine()

	alphas := []float64{0.05, 0.2, 0.5, 0.8}
	type row struct {
		Alpha     float64
		Target    string
		PeakDepth int
		Backlog   int
		DrainedMs float64
		Drained   bool
		MeanMs    float64
	}
	var rows []row
	for i, alpha := range alphas {
		spec := ewmaWithAlpha(alpha)
		exp := engine.Experiment{ID: fmt.Sprintf("015b-f3-alpha%.2f", alpha), Scenario: scenario, Policy: spec}
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, spec)
		}
		if err != nil {
			log.Fatalf("F3 alpha=%.2f: %v", alpha, err)
		}
		wr := result.WorldResult
		target, peak, committed, _, drainFound, drainAt, mean, _ := bottleneckAndMetrics(wr, targets, 1, float64(horizon.Milliseconds()))
		rows = append(rows, row{Alpha: alpha, Target: target, PeakDepth: peak, Backlog: committed, DrainedMs: drainAt, Drained: drainFound, MeanMs: mean})
		fmt.Printf("  alpha=%.2f  bottleneck=%s  peak_depth=%d  committed_backlog=%d  drain_found=%v(@%.0fms)  mean=%.2fms\n",
			alpha, target, peak, committed, drainFound, drainAt, mean)
	}
	monotonic := true
	for i := 1; i < len(rows); i++ {
		if rows[i].Backlog > rows[i-1].Backlog {
			monotonic = false
		}
	}
	fmt.Printf("  RESULT: committed backlog decreases monotonically as alpha increases: %v\n", monotonic)
	fmt.Println("  (contrast with experiment-014g's own finding: alpha had NO effect on the main boundary's MEAN LATENCY outcome --")
	fmt.Println("   this checks whether it nonetheless affects committed backlog specifically, a finer-grained measurement 014g never had.)")
}

// --- F4: concentration shape (bimodal group vs. graduated single-target) ---

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

func runF4() {
	fmt.Println("\n=== F4: Concentration Shape -- Bimodal Group Lock-in vs. Graduated Single-Target Lock-in ===")
	horizon := 8 * time.Second
	capacity := 1

	graduated := canonicalTargets()
	bimodal := bimodalTargets(3, 2, capacity) // 5 targets total, matching graduated's own count

	for _, cfg := range []struct {
		name    string
		targets []replay.TargetProfile
	}{{"graduated", graduated}, {"bimodal", bimodal}} {
		seeds := replay.DeriveSeeds(15400)
		arrivals := canonicalArrivals(seeds, horizon, 0)
		scenario := replay.Scenario{Targets: cfg.targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: "015b-f4-" + cfg.name, Scenario: scenario, Policy: replay.EWMAPolicy()}
		result, err := v.Run(exp)
		if err != nil {
			log.Fatalf("F4 %s: %v", cfg.name, err)
		}
		wr := result.WorldResult
		conc := backlog.ComputeConcentration(wr.CompletedByTarget)
		target, peak, committed, congestionFound, drainFound, drainAt, mean, p99 := bottleneckAndMetrics(wr, cfg.targets, capacity, float64(horizon.Milliseconds()))
		fmt.Printf("  %-10s top1=%.3f  bottleneck=%s  peak_depth=%d  congestion=%v  committed_backlog=%d  drain=%v(@%.0fms)  mean=%.2fms  p99=%.2fms\n",
			cfg.name, conc.Top1Share, target, peak, congestionFound, committed, drainFound, drainAt, mean, p99)
	}
	fmt.Println("  RESULT: see whether EWMA's committed-backlog collapse mechanism appears under BOTH concentration shapes,")
	fmt.Println("  or only the graduated single-target-lock-in shape experiment-015a already demonstrated.")
}

// --- F6: P2C vs. EWMA across independent seeds ---

func runF6() {
	fmt.Println("\n=== F6: P2C vs. EWMA -- Structural Difference or Lucky Seed? ===")
	horizon := 8 * time.Second
	targets := canonicalTargets()

	type row struct {
		Seed             int64
		Policy           string
		PeakDepth        int
		CommittedBacklog int
		DrainFound       bool
		MeanMs           float64
	}
	var rows []row
	const seedCount = 8
	for i := 0; i < seedCount; i++ {
		rootSeed := int64(15600 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals := canonicalArrivals(seeds, horizon, 0.3)
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("015b-f6-seed%d", rootSeed), Scenario: scenario, Policy: replay.EWMAPolicy()}

		rEwma, err := v.Run(exp)
		if err != nil {
			log.Fatalf("F6 seed=%d ewma: %v", rootSeed, err)
		}
		rP2C, err := v.Replay(exp, replay.P2CLoadPolicy())
		if err != nil {
			log.Fatalf("F6 seed=%d p2c: %v", rootSeed, err)
		}
		for _, pr := range []struct {
			name string
			wr   *replay.WorldResult
		}{{"ewma", rEwma.WorldResult}, {"p2c-load", rP2C.WorldResult}} {
			_, peak, committed, _, drainFound, _, mean, _ := bottleneckAndMetrics(pr.wr, targets, 1, float64(horizon.Milliseconds()))
			rows = append(rows, row{Seed: rootSeed, Policy: pr.name, PeakDepth: peak, CommittedBacklog: committed, DrainFound: drainFound, MeanMs: mean})
		}
	}

	fmt.Println("  seed       policy       peak_depth  committed_backlog  drain_found  mean_ms")
	for _, r := range rows {
		fmt.Printf("  %-10d %-12s %-11d %-18d %-12v %.2f\n", r.Seed, r.Policy, r.PeakDepth, r.CommittedBacklog, r.DrainFound, r.MeanMs)
	}

	ewmaWorst, p2cWorst := 0, 0
	for _, r := range rows {
		if r.Policy == "ewma" && r.CommittedBacklog > 50 {
			ewmaWorst++
		}
		if r.Policy == "p2c-load" && r.CommittedBacklog > 50 {
			p2cWorst++
		}
	}
	fmt.Printf("\n  RESULT: EWMA showed committed_backlog>50 in %d/%d seeds; P2C-load in %d/%d seeds.\n", ewmaWorst, seedCount, p2cWorst, seedCount)
	if ewmaWorst > p2cWorst {
		fmt.Println("  EWMA's catastrophic commitment is a STRUCTURAL, seed-independent property, not a one-off; P2C's own")
		fmt.Println("  advantage (if consistent) supports a real mechanistic difference (sampling never fully commits),")
		fmt.Println("  not merely a lucky draw in the canonical scenario's own seed.")
	} else {
		fmt.Println("  P2C shows the same failure mode as EWMA often enough that the 'sampling avoids lock-in' distinction")
		fmt.Println("  does not hold up across seeds -- experiment-015a's result may have been seed-specific.")
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-B: Falsification Program (F1, F3, F4, F6)")
	fmt.Println(" F2 and F5 are answered directly by experiment-015a's own canonical-scenario data.")
	fmt.Println("=====================================================================================")

	runF1()
	runF3()
	runF4()
	runF6()

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Note       string `json:"note"`
	}{Experiment: "015-B-falsification-program", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Note: "primary output is console-printed analysis for F1/F3/F4/F6; see stdout captured in the Stage 15 writeup"}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015B-falsification-program.json"), b, 0644)
	fmt.Println("\nExperiment 015-B complete.")
}
