package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"flashflow/internal/proxy"
	"flashflow/internal/tuning"
)

const outDirName = "experiments/008-tuning-validation/results"

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 008-B: Random Search Tuner v1")
	fmt.Println(" Can automated configuration selection beat the hand-chosen default AdaptiveConfig on the")
	fmt.Println(" Development scenario set, without ever touching Holdout?")
	fmt.Println("==========================================================================================")

	split := tuning.NewSplit(tuning.DefaultScenarioSpace())
	fmt.Printf("\nDevelopment set: %d scenarios (seeds %d-%d). Holdout set generated but NOT used here -- 008-C's job.\n",
		len(split.Development), tuning.DevelopmentSeedStart, tuning.DevelopmentSeedStart+int64(len(split.Development))-1)

	// Baseline: the hand-chosen proxy.DefaultAdaptiveConfig(), scored on
	// the identical Development set the search will use -- the fair
	// comparison master context rule 4 asks for ("can automated
	// configuration selection beat a hand-chosen configuration").
	baselineCfg := proxy.DefaultAdaptiveConfig()
	baselineMetrics, baselineScores, err := tuning.Evaluate(baselineCfg, split.Development)
	if err != nil {
		log.Fatalf("evaluating baseline config: %v", err)
	}
	baselineUtility := tuning.Utility(baselineScores, tuning.DefaultObjectiveWeights())
	fmt.Printf("\nBaseline (hand-chosen default): utility=%.4f  meanLatency=%.1fms  p99=%.1fms  reject=%.3f  maxShare=%.3f\n",
		baselineUtility, baselineMetrics.MeanLatencyMs, baselineMetrics.P99LatencyMs, baselineMetrics.RejectedRate, baselineMetrics.MeanMaxShare)

	rsc := tuning.DefaultRandomSearchConfig()
	fmt.Printf("\nRunning Random Search: %d evaluations, optimizer seed %d, tuner version %s...\n",
		rsc.Evaluations, rsc.OptimizerSeed, tuning.TunerVersion)

	start := time.Now()
	result := tuning.RunRandomSearch(rsc, split.Development)
	elapsed := time.Since(start)

	best, ok := result.Best()
	if !ok {
		log.Fatal("random search produced no valid evaluation at all")
	}

	fmt.Printf("Search complete in %v (%d evaluations, %d cache hits).\n", elapsed, len(result.Evaluations), countCacheHits(result.Evaluations))
	fmt.Printf("\nBest found (evaluation #%d, config %s): utility=%.4f  meanLatency=%.1fms  p99=%.1fms  reject=%.3f  maxShare=%.3f\n",
		best.Index, best.ConfigHash, best.Utility, best.Metrics.MeanLatencyMs, best.Metrics.P99LatencyMs, best.Metrics.RejectedRate, best.Metrics.MeanMaxShare)
	fmt.Printf("Best config weights: load=%.3f latency=%.3f cache=%.3f cost=%.3f  ReferenceLatency=%v  StaleAfter=%v\n",
		best.Config.Weights.Load, best.Config.Weights.Latency, best.Config.Weights.Cache, best.Config.Weights.Cost,
		best.Config.ReferenceLatency, best.Config.StaleAfter)

	fmt.Printf("\nConvergence: last improved at evaluation #%d (of %d); plateaued over final %.0f%%: %v\n",
		result.Convergence.LastImprovedAtIndex, len(result.Evaluations), result.Convergence.PlateauWindowFraction*100, result.Convergence.Plateaued)

	improvement := best.Utility - baselineUtility
	fmt.Printf("\nImprovement over hand-chosen default: %+.4f utility (%.2f%% relative)\n", improvement, 100*improvement/baselineUtility)

	// Top 5 for a human-readable ledger excerpt in the printed output;
	// the full ledger (all 200 evaluations, nothing discarded) still
	// goes to its own file below.
	ranked := append([]tuning.Evaluation(nil), result.Evaluations...)
	sort.Slice(ranked, func(i, j int) bool {
		if !ranked[i].Valid {
			return false
		}
		if !ranked[j].Valid {
			return true
		}
		return ranked[i].Utility > ranked[j].Utility
	})
	fmt.Println("\nTop 5 candidates:")
	for i := 0; i < 5 && i < len(ranked); i++ {
		e := ranked[i]
		fmt.Printf("  #%d (eval %d, %s): utility=%.4f  meanLatency=%.1fms  p99=%.1fms  reject=%.3f  maxShare=%.3f\n",
			i+1, e.Index, e.ConfigHash, e.Utility, e.Metrics.MeanLatencyMs, e.Metrics.P99LatencyMs, e.Metrics.RejectedRate, e.Metrics.MeanMaxShare)
	}

	finding := fmt.Sprintf(
		"Random Search v1 (%d evaluations, optimizer seed %d, entirely separate from any Scenario's own exogenous "+
			"seed or any statistics-analysis seed) drew candidates from the 3-dimensional weight simplex plus "+
			"ReferenceLatency/StaleAfter, scoring each against the identical 40-scenario Development set the "+
			"hand-chosen default was also scored against -- never touching the 20-scenario Holdout set, reserved "+
			"entirely for 008-C. The best candidate found (utility %.4f) improved on the hand-chosen default "+
			"(utility %.4f) by %.4f (%.2f%% relative) on Development. The search's own convergence curve shows the "+
			"best-so-far value last improved at evaluation #%d of %d, %s in the final %.0f%% of the run -- this "+
			"Development-set improvement is NOT yet evidence of a better configuration in any general sense: master "+
			"context rule 9 is explicit that the Development improvement alone proves nothing about generalization, "+
			"only that the search space and objective function are non-trivial to explore. 008-C evaluates this "+
			"exact winning candidate against Holdout -- once, and only once -- to find out whether this Development "+
			"gain survives contact with scenarios the search never saw.",
		rsc.Evaluations, rsc.OptimizerSeed, best.Utility, baselineUtility, improvement, 100*improvement/baselineUtility,
		result.Convergence.LastImprovedAtIndex, len(result.Evaluations),
		plateauWord(result.Convergence.Plateaued), result.Convergence.PlateauWindowFraction*100,
	)
	fmt.Printf("\n%s\n", finding)

	summary := struct {
		Experiment              string                    `json:"experiment"`
		Timestamp               string                    `json:"timestamp"`
		DevelopmentSize         int                       `json:"development_size"`
		HoldoutSize             int                       `json:"holdout_size_generated_not_used"`
		TunerVersion            string                    `json:"tuner_version"`
		OptimizerSeed           int64                     `json:"optimizer_seed"`
		Evaluations             int                       `json:"evaluations"`
		CacheHits               int                       `json:"cache_hits"`
		BaselineConfig          proxy.AdaptiveConfig      `json:"baseline_config"`
		BaselineMetrics         tuning.Metrics            `json:"baseline_metrics"`
		BaselineUtility         float64                   `json:"baseline_utility"`
		BestEvaluation          tuning.Evaluation         `json:"best_evaluation"`
		ImprovementOverBaseline float64                   `json:"improvement_over_baseline"`
		Convergence             tuning.ConvergenceSummary `json:"convergence"`
		ElapsedSeconds          float64                   `json:"elapsed_seconds"`
		Findings                string                    `json:"findings"`
	}{
		Experiment: "008-B-random-search-tuner", Timestamp: time.Now().UTC().Format(time.RFC3339),
		DevelopmentSize: len(split.Development), HoldoutSize: len(split.Holdout),
		TunerVersion: result.TunerVersion, OptimizerSeed: result.OptimizerSeed,
		Evaluations: len(result.Evaluations), CacheHits: countCacheHits(result.Evaluations),
		BaselineConfig: baselineCfg, BaselineMetrics: baselineMetrics, BaselineUtility: baselineUtility,
		BestEvaluation: best, ImprovementOverBaseline: improvement,
		Convergence: result.Convergence, ElapsedSeconds: elapsed.Seconds(), Findings: finding,
	}
	sb, _ := json.MarshalIndent(summary, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "008B-random-search-summary.json"), sb, 0644)

	// The full ledger -- every one of the 200 evaluations, nothing
	// discarded (master context rule 19) -- as its own file, since it's
	// the artifact 008-C's sensitivity analysis and any later
	// generalization-gap reporting reads back in.
	lb, _ := json.MarshalIndent(result, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "008B-search-ledger.json"), lb, 0644)

	fmt.Println("\nExperiment 008-B complete.")
}

func countCacheHits(evals []tuning.Evaluation) int {
	n := 0
	for _, e := range evals {
		if e.CacheHit {
			n++
		}
	}
	return n
}

func plateauWord(p bool) string {
	if p {
		return "plateaued"
	}
	return "still improving"
}
