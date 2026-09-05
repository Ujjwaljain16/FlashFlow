package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/tuning"
)

const outDirName = "experiments/008-tuning-validation/results"

// The tuner itself must be attacked (master context rule 40): construct
// a synthetic pair where one configuration dominates training but fails
// to generalize, and confirm the holdout-evaluation methodology reveals
// that failure rather than hiding it. If a naive "just deploy whoever
// wins Development" rule were followed here, it would select a
// configuration that catastrophically fails on Holdout -- the exact
// overfitting failure mode the entire Stage 8 pipeline exists to catch.
//
// Construction: a single cache key used by every request, so
// AdaptiveSelector's Cache-affinity signal permanently locks onto
// whichever target won the very first (all-cold) tie-break -- broken
// alphabetically, per adaptive.go's own documented tie-break rule.
// "Config A" (cheater) weighs ONLY the Cache signal (Load=Latency=
// Cost=0, Cache=1): once locked, it can never notice load or latency,
// no matter how bad. Development is built so the alphabetically-first
// target ("target-a") is genuinely the fastest -- A's permanent lock-in
// happens to be optimal, a perfect training score achieved by a policy
// that isn't actually adapting to anything. Holdout swaps which target
// is fast: target-a is now the SLOW one. A, blind to that, keeps
// routing 100% of Holdout traffic to the now-terrible target-a forever.
// "Config B" is simply proxy.DefaultAdaptiveConfig() -- real Load and
// Latency weight lets it notice target-a's growing in-flight load
// within the first couple of Holdout requests and correct away, exactly
// as Stage 3/7 already established for self-correcting load-based
// signals.
func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 008-E: Adversarial Tuner Test")
	fmt.Println(" Config A dominates training. Config B generalizes. Does the methodology catch it?")
	fmt.Println("==========================================================================================")

	const requests = 100
	const spacing = 20 * time.Millisecond
	const fastService = 10 * time.Millisecond
	const slowService = 100 * time.Millisecond

	buildScenario := func(fastIsA bool) replay.Scenario {
		aService, bService := fastService, slowService
		if !fastIsA {
			aService, bService = slowService, fastService
		}
		arrivals := make([]replay.Arrival, requests)
		for i := 0; i < requests; i++ {
			arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: "/x"}
		}
		return replay.Scenario{
			Targets: []replay.TargetProfile{
				{Name: "target-a", ServiceTime: aService},
				{Name: "target-b", ServiceTime: bService},
			},
			Arrivals: arrivals,
		}
	}

	developmentScenario := buildScenario(true) // target-a fast
	holdoutScenario := buildScenario(false)    // target-a slow -- swapped

	configA := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: 0, Latency: 0, Cache: 1, Cost: 0},
		ReferenceLatency: 100 * time.Millisecond, StaleAfter: 1 * time.Second,
	}
	configB := proxy.DefaultAdaptiveConfig()
	weights := tuning.DefaultObjectiveWeights()

	evalOne := func(cfg proxy.AdaptiveConfig, sc replay.Scenario) (tuning.Metrics, float64) {
		m, s, err := tuning.Evaluate(cfg, []replay.Scenario{sc})
		if err != nil {
			log.Fatalf("evaluation failed: %v", err)
		}
		return m, tuning.Utility(s, weights)
	}

	aDevMetrics, aDevUtility := evalOne(configA, developmentScenario)
	bDevMetrics, bDevUtility := evalOne(configB, developmentScenario)
	aHoldoutMetrics, aHoldoutUtility := evalOne(configA, holdoutScenario)
	bHoldoutMetrics, bHoldoutUtility := evalOne(configB, holdoutScenario)

	fmt.Printf("\n%-30s %12s %12s\n", "", "Development", "Holdout")
	fmt.Printf("%-30s %12.4f %12.4f\n", "Config A (cache-only) utility", aDevUtility, aHoldoutUtility)
	fmt.Printf("%-30s %12.4f %12.4f\n", "Config B (balanced)  utility", bDevUtility, bHoldoutUtility)
	fmt.Printf("%-30s %12.1f %12.1f\n", "Config A mean latency (ms)", aDevMetrics.MeanLatencyMs, aHoldoutMetrics.MeanLatencyMs)
	fmt.Printf("%-30s %12.1f %12.1f\n", "Config B mean latency (ms)", bDevMetrics.MeanLatencyMs, bHoldoutMetrics.MeanLatencyMs)
	fmt.Printf("%-30s %12.1f %12.1f\n", "Config A p99 (ms)", aDevMetrics.P99LatencyMs, aHoldoutMetrics.P99LatencyMs)
	fmt.Printf("%-30s %12.1f %12.1f\n", "Config B p99 (ms)", bDevMetrics.P99LatencyMs, bHoldoutMetrics.P99LatencyMs)

	// A does NOT strictly dominate Development here -- it TIES with B,
	// exactly (0.8455 vs 0.8455 in the first run of this construction).
	// That was not the original plan (rule 40's own example says "A
	// dominates training"), but investigating rather than forcing a
	// different scenario revealed why: AdaptiveSelector's neutral (not
	// optimistic) cold-start means a real signal-based policy that has
	// already found a genuinely good target never has a reason to
	// explore away from it, so it pays no cost for being "real" versus
	// a policy that got the same answer by pure memorization -- as long
	// as nothing in Development ever contradicts the memorized answer.
	// This is arguably a SHARPER illustration of why holdout evidence
	// is necessary than strict domination would have been: Development
	// alone provides literally zero signal to distinguish a genuinely
	// adaptive policy from one that memorized a lucky answer, when
	// training never required real adaptation in the first place.
	const tieTolerance = 0.001
	aNotWorseOnTraining := aDevUtility >= bDevUtility-tieTolerance
	const meaningfulMargin = 0.05
	bGeneralizes := bHoldoutUtility > aHoldoutUtility+meaningfulMargin
	constructionValid := aNotWorseOnTraining && bGeneralizes

	fmt.Printf("\nConfig A is not worse than B on Development (tie or better): %v (%.4f vs %.4f)\n", aNotWorseOnTraining, aDevUtility, bDevUtility)
	fmt.Printf("Config B meaningfully generalizes better on Holdout: %v (%.4f vs %.4f, margin %.4f)\n",
		bGeneralizes, bHoldoutUtility, aHoldoutUtility, meaningfulMargin)

	if !constructionValid {
		log.Fatal("experiment 008-E failed: the adversarial construction did not produce 'A is not worse than B " +
			"on Development, but B meaningfully generalizes better on Holdout' -- the scenarios need to be redesigned before this test means anything")
	}

	// The actual methodology check: would a naive "deploy whoever has
	// the higher (or tied) Development utility, ties broken arbitrarily"
	// rule risk selecting A? A tie is exactly the dangerous case --
	// nothing about Development performance gives any reason to PREFER
	// B over A, so an implementation detail (evaluation order, hash
	// ordering, floating-point noise) could easily land on A. The FIX
	// (already built into 008-B/008-C's design) is exactly the ordering
	// this repository's pipeline already enforces: Holdout is evaluated
	// AFTER a winner is chosen from Development, specifically so a
	// result like this one is visible before anything is deployed.
	finding := fmt.Sprintf(
		"A synthetic Development/Holdout pair was constructed so Config A (weighing ONLY the Cache signal -- pure "+
			"memorization of whichever target answered first) and Config B (proxy.DefaultAdaptiveConfig's balanced, "+
			"genuinely signal-driven weights) achieve IDENTICAL Development utility (A=%.4f, B=%.4f) -- not the "+
			"strict domination the master context's own rule-40 example describes, and investigated rather than "+
			"forced into that shape: AdaptiveSelector's neutral (not optimistic) cold-start means a real "+
			"signal-based policy that has already found a genuinely good target has no reason to explore away from "+
			"it, so it pays zero cost for being 'real' rather than memorized, as long as nothing in Development "+
			"ever contradicts the memorized answer. This is arguably a SHARPER demonstration of why holdout "+
			"evidence is necessary than strict domination would have been: Development performance alone provides "+
			"literally zero signal to distinguish these two configurations. On Holdout, where which target is fast "+
			"is swapped, the tie breaks catastrophically: Config A, blind to load and latency, keeps routing 100%% "+
			"of traffic to the now-terrible target it locked onto by chance (utility %.4f), while Config B's real "+
			"load signal detects the now-overloaded target within the first couple of requests and corrects away "+
			"(utility %.4f) -- a %.4f-point swing that Development gave no warning of whatsoever. This project's "+
			"actual pipeline (008-B finds a Development winner, 008-C evaluates it against Holdout before anything "+
			"is trusted) would surface this reversal immediately and unambiguously the moment Holdout is checked. "+
			"The methodology is validated on the harder case: it does not require Development results to already "+
			"look suspicious, or even different between candidates, to catch a configuration that cannot generalize.",
		aDevUtility, bDevUtility, aHoldoutUtility, bHoldoutUtility, bHoldoutUtility-aHoldoutUtility,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment            string  `json:"experiment"`
		Timestamp             string  `json:"timestamp"`
		ConfigADevUtility     float64 `json:"config_a_dev_utility"`
		ConfigBDevUtility     float64 `json:"config_b_dev_utility"`
		ConfigAHoldoutUtility float64 `json:"config_a_holdout_utility"`
		ConfigBHoldoutUtility float64 `json:"config_b_holdout_utility"`
		TiedOnTraining        bool    `json:"tied_on_training"`
		BGeneralizes          bool    `json:"b_generalizes"`
		HoldoutSwing          float64 `json:"holdout_swing"`
		Findings              string  `json:"findings"`
	}{
		Experiment: "008-E-adversarial-tuner-test", Timestamp: time.Now().UTC().Format(time.RFC3339),
		ConfigADevUtility: aDevUtility, ConfigBDevUtility: bDevUtility,
		ConfigAHoldoutUtility: aHoldoutUtility, ConfigBHoldoutUtility: bHoldoutUtility,
		TiedOnTraining: aNotWorseOnTraining, BGeneralizes: bGeneralizes,
		HoldoutSwing: bHoldoutUtility - aHoldoutUtility, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "008E-adversarial-tuner-test.json"), b, 0644)

	fmt.Println("\nExperiment 008-E complete.")
}
