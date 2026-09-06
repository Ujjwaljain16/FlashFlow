// Command experiment-011b is Stage 11's Program B: deliberate adversarial
// scenarios constructed to make Adaptive lose, or at least reveal a real
// mechanism weakness -- not a generic sweep. Each scenario states its
// hypothesized failure mechanism up front and reports whatever actually
// happened, including a negative result if Adaptive doesn't fail as
// predicted (per Stage11's own explicit instruction: a scenario that
// doesn't produce the expected failure is a result, not a reason to keep
// redesigning until one does).
//
// H2 (a latency-oscillation/staleness attack -- "the best target changes
// faster than StaleAfter allows detection") is NOT implemented here: it
// would require a per-target service time that changes mid-run, and
// internal/replay.TargetProfile has no such mechanism (ServiceTime is one
// fixed value for the whole Scenario). This is reported as a genuine
// platform-capability gap in docs/StageArtifacts/Stage11.md rather than
// faked via an unrelated proxy mechanism.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/011-research-validation/results"

type ScenarioMetrics struct {
	Scenario     string  `json:"scenario"`
	Policy       string  `json:"policy"`
	Completed    int     `json:"completed"`
	Rejected     int     `json:"rejected"`
	MeanMs       float64 `json:"mean_ms"`
	P99Ms        float64 `json:"p99_ms"`
	MaxShare     float64 `json:"max_share"`
	AffinityHits int     `json:"affinity_target_share_pct,omitempty"` // % of hot-key completions served by the ORIGINAL affinity target after it recovers (B1 only)
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

// b1Policies additionally includes an ablation for the B1 scenario only:
// AdaptivePolicy with its cache-affinity weight forced to 0, everything
// else at DefaultAdaptiveConfig's values -- direct confirmation (not
// inference) that the affinity term specifically is what causes the
// lock-in, per B1's own "What Would Falsify This" table.
func b1Policies() []struct {
	name string
	spec replay.PolicySpec
} {
	noCacheCfg := proxy.DefaultAdaptiveConfig()
	noCacheCfg.Weights.Cache = 0
	specs := policySpecs()
	return append(specs, struct {
		name string
		spec replay.PolicySpec
	}{"adaptive-no-cache-weight", replay.AdaptivePolicyWithConfig(noCacheCfg)})
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("==========================================================")
	fmt.Println(" Experiment 011-B: Adversarial Adaptive Testing")
	fmt.Println("==========================================================")

	var all []ScenarioMetrics
	all = append(all, runCacheAffinityDeception()...)
	all = append(all, runCorrelatedFailure()...)

	out := struct {
		Experiment string            `json:"experiment"`
		Timestamp  string            `json:"timestamp"`
		Runs       []ScenarioMetrics `json:"runs"`
	}{Experiment: "011-B-adversarial-adaptive", Timestamp: time.Now().UTC().Format(time.RFC3339), Runs: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011B-adversarial-adaptive.json"), b, 0644)
	fmt.Println("\nExperiment 011-B complete.")
}

// runCacheAffinityDeception is H3: Adaptive's cache-affinity signal
// (weight 0.1) should initially route a hot key to the fastest target
// (edge-a, 15ms). edge-a then crashes, forcing the hot key onto a slower
// target (edge-b or edge-c), which becomes the new affinity target.
// edge-a recovers and is objectively best again -- the question is
// whether cache-affinity stickiness measurably delays Adaptive's return
// to edge-a relative to a policy with no notion of affinity at all
// (round-robin) or one driven by latency alone (ewma).
//
// Hypothesized mechanism: affinity contributes a fixed 0.1 score to
// whichever target last served the hot key, regardless of that target's
// CURRENT latency. If the latency gap between edge-a (recovered, 15ms)
// and the interim target (30-60ms) isn't large enough to overcome a 0.1
// score gap once weighted, Adaptive should show a MEASURABLY higher
// share of post-recovery hot-key traffic still going to the interim
// target than a latency-only policy (ewma) does -- that's the falsifiable
// prediction, checked directly below rather than assumed.
func runCacheAffinityDeception() []ScenarioMetrics {
	fmt.Println("\n--- B1: Cache-Affinity Deception (H3) ---")
	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(3011)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("B1: generating traffic: %v", err)
	}
	sched, err := chaos.ParseYAML(strings.NewReader(
		"- at: 1s\n  target: edge-a\n  action: crash\n- at: 2s\n  target: edge-a\n  action: recover\n"))
	if err != nil {
		log.Fatalf("B1: parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("B1: compiling chaos: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "011b-cache-affinity-deception", Scenario: scenario}

	var out []ScenarioMetrics
	for i, ps := range b1Policies() {
		exp.Policy = ps.spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, ps.spec)
		}
		if err != nil {
			log.Fatalf("B1 %s: %v", ps.name, err)
		}
		wr := result.WorldResult
		m := metricsFromWorld("B1-cache-affinity-deception", ps.name, wr)

		// Post-recovery share: of completions AFTER t=2s (edge-a's
		// recovery) that were dispatched to the hot key, what fraction
		// went to edge-a (the objectively best, recovered target) vs
		// stayed on the interim target? Uses SelectionRecord.VirtualTimeMs
		// and Key/Target, not Completions, since the decision (not the
		// completion time) is what the affinity mechanism actually acts
		// on.
		hotKey := hottestKey(wr.Records)
		postRecovery, toA := 0, 0
		for _, r := range wr.Records {
			if r.Key != hotKey || r.VirtualTimeMs < 2000 {
				continue
			}
			postRecovery++
			if r.Target == "edge-a" {
				toA++
			}
		}
		pct := 0
		if postRecovery > 0 {
			pct = int(100 * float64(toA) / float64(postRecovery))
		}
		m.AffinityHits = pct
		out = append(out, m)
		fmt.Printf("  %-14s completed=%-4d mean=%.2fms  post-recovery hot-key share to recovered edge-a=%d%% (of %d post-recovery hot-key decisions)\n",
			ps.name, m.Completed, m.MeanMs, pct, postRecovery)
	}
	return out
}

func hottestKey(records []replay.SelectionRecord) string {
	counts := map[string]int{}
	for _, r := range records {
		counts[r.Key]++
	}
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

// runCorrelatedFailure is a signal-scarcity adversarial case: two of
// three targets crash simultaneously, leaving only one survivor for a
// window. Hypothesized mechanism: with only one available target, EVERY
// policy's selection signal is moot (available has exactly one element),
// so no policy should meaningfully outperform another DURING the outage
// -- the interesting question is whether Adaptive's multi-signal decision
// process imposes measurable overhead or behaves worse than round-robin
// once multiple targets recover simultaneously and must be re-balanced.
func runCorrelatedFailure() []ScenarioMetrics {
	fmt.Println("\n--- B2: Correlated Failure (signal scarcity) ---")
	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 20 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 25 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 30 * time.Millisecond},
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(3012)
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.3),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("B2: generating traffic: %v", err)
	}
	sched, err := chaos.ParseYAML(strings.NewReader(
		"- at: 1.5s\n  target: edge-a\n  action: crash\n- at: 1.5s\n  target: edge-b\n  action: crash\n" +
			"- at: 2.5s\n  target: edge-a\n  action: recover\n- at: 2.5s\n  target: edge-b\n  action: recover\n"))
	if err != nil {
		log.Fatalf("B2: parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("B2: compiling chaos: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "011b-correlated-failure", Scenario: scenario}

	var out []ScenarioMetrics
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
			log.Fatalf("B2 %s: %v", ps.name, err)
		}
		m := metricsFromWorld("B2-correlated-failure", ps.name, result.WorldResult)
		out = append(out, m)
		fmt.Printf("  %-14s completed=%-4d rejected=%-4d mean=%.2fms  p99=%.2fms  max_share=%.3f\n",
			ps.name, m.Completed, m.Rejected, m.MeanMs, m.P99Ms, m.MaxShare)
	}
	return out
}

func metricsFromWorld(scenario, policy string, wr *replay.WorldResult) ScenarioMetrics {
	m := ScenarioMetrics{Scenario: scenario, Policy: policy}
	m.Completed = len(wr.Completions)
	m.Rejected = wr.RejectedCount
	if m.Completed > 0 {
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		m.MeanMs, _ = statistics.Mean(ms)
		m.P99Ms, _ = statistics.Percentile(ms, 99)
		maxCount := 0
		for _, c := range wr.CompletedByTarget {
			if c > maxCount {
				maxCount = c
			}
		}
		m.MaxShare = float64(maxCount) / float64(m.Completed)
	}
	return m
}
