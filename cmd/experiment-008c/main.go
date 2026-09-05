package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/proxy"
	"flashflow/internal/statistics"
	"flashflow/internal/tuning"
)

const outDirName = "experiments/008-tuning-validation/results"

// This experiment is the holdout step itself: the winning candidate
// from 008-B (reproduced here by rerunning the identical, seeded Random
// Search -- see the note below) is evaluated against Holdout scenarios
// for the first and only time. Per the master context's own sacred
// rule, this evaluation happens exactly once and its result is recorded
// as-is, whatever it says -- there is no second attempt, no manual
// re-selection after looking, and no regenerating Holdout to chase a
// better number.
func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 008-C: Holdout Validation & Generalization Gap")
	fmt.Println(" Does 008-B's winning candidate's Development-set improvement survive contact with scenarios")
	fmt.Println(" the search never saw?")
	fmt.Println("==========================================================================================")

	split := tuning.NewSplit(tuning.DefaultScenarioSpace())

	// Reproducing 008-B's search here, rather than reading its JSON
	// output, is a deliberate determinism check as much as a
	// convenience: RunRandomSearch is seeded (OptimizerSeed) and scored
	// against a seeded Development set, so rerunning it must reproduce
	// the identical winner byte-for-byte, or something in the pipeline
	// isn't as deterministic as claimed. If this ever diverges from
	// 008B-random-search-summary.json's recorded best_evaluation, that
	// is itself a finding to investigate, not to paper over.
	rsc := tuning.DefaultRandomSearchConfig()
	searchResult := tuning.RunRandomSearch(rsc, split.Development)
	winner, ok := searchResult.Best()
	if !ok {
		log.Fatal("random search produced no valid evaluation to validate")
	}
	fmt.Printf("\nReproduced 008-B's winner: config %s, Development utility %.4f\n", winner.ConfigHash, winner.Utility)

	baselineCfg := proxy.DefaultAdaptiveConfig()
	weights := tuning.DefaultObjectiveWeights()

	// --- Pooled utility on Development and Holdout, for both configs. ---
	_, baselineDevScores, err := tuning.Evaluate(baselineCfg, split.Development)
	if err != nil {
		log.Fatalf("evaluating baseline on development: %v", err)
	}
	baselineDevUtility := tuning.Utility(baselineDevScores, weights)

	baselineHoldoutMetrics, baselineHoldoutScores, err := tuning.Evaluate(baselineCfg, split.Holdout)
	if err != nil {
		log.Fatalf("evaluating baseline on holdout: %v", err)
	}
	baselineHoldoutUtility := tuning.Utility(baselineHoldoutScores, weights)

	winnerHoldoutMetrics, winnerHoldoutScores, err := tuning.Evaluate(winner.Config, split.Holdout)
	if err != nil {
		log.Fatalf("evaluating winner on holdout: %v", err)
	}
	winnerHoldoutUtility := tuning.Utility(winnerHoldoutScores, weights)

	trainingImprovement := winner.Utility - baselineDevUtility
	holdoutImprovement := winnerHoldoutUtility - baselineHoldoutUtility
	generalizationGap := trainingImprovement - holdoutImprovement

	fmt.Printf("\n%-28s %10s %10s\n", "", "Development", "Holdout")
	fmt.Printf("%-28s %10.4f %10.4f\n", "Baseline utility", baselineDevUtility, baselineHoldoutUtility)
	fmt.Printf("%-28s %10.4f %10.4f\n", "Winner utility", winner.Utility, winnerHoldoutUtility)
	fmt.Printf("%-28s %+10.4f %+10.4f\n", "Improvement over baseline", trainingImprovement, holdoutImprovement)
	fmt.Printf("\nGeneralization gap (training improvement - holdout improvement): %+.4f\n", generalizationGap)

	// --- Robustness over scenarios (mean/median/worst/stddev), not
	// just the pooled aggregate -- master context rule 22. Computed for
	// both configs on both sets, so a "the winner looks fine on
	// average" claim can be checked against its worst case too. ---
	baselineDevPer, err := tuning.PerScenarioUtility(baselineCfg, split.Development, weights)
	if err != nil {
		log.Fatalf("per-scenario utility (baseline, dev): %v", err)
	}
	winnerDevPer, err := tuning.PerScenarioUtility(winner.Config, split.Development, weights)
	if err != nil {
		log.Fatalf("per-scenario utility (winner, dev): %v", err)
	}
	baselineHoldoutPer, err := tuning.PerScenarioUtility(baselineCfg, split.Holdout, weights)
	if err != nil {
		log.Fatalf("per-scenario utility (baseline, holdout): %v", err)
	}
	winnerHoldoutPer, err := tuning.PerScenarioUtility(winner.Config, split.Holdout, weights)
	if err != nil {
		log.Fatalf("per-scenario utility (winner, holdout): %v", err)
	}

	baselineDevRobust, _ := tuning.ComputeRobustness(baselineDevPer)
	winnerDevRobust, _ := tuning.ComputeRobustness(winnerDevPer)
	baselineHoldoutRobust, _ := tuning.ComputeRobustness(baselineHoldoutPer)
	winnerHoldoutRobust, _ := tuning.ComputeRobustness(winnerHoldoutPer)

	fmt.Println("\nRobustness over scenarios (per-scenario utility distribution):")
	printRobustness := func(label string, r tuning.RobustnessSummary) {
		fmt.Printf("  %-28s mean=%.4f median=%.4f worst=%.4f stddev=%.4f\n", label, r.Mean, r.Median, r.Worst, r.StdDev)
	}
	printRobustness("Baseline, Development", baselineDevRobust)
	printRobustness("Winner,   Development", winnerDevRobust)
	printRobustness("Baseline, Holdout", baselineHoldoutRobust)
	printRobustness("Winner,   Holdout", winnerHoldoutRobust)

	worstCaseRegression := winnerHoldoutRobust.Worst - baselineHoldoutRobust.Worst
	fmt.Printf("\nWorst-case regression on Holdout (winner's worst scenario - baseline's worst scenario): %+.4f\n", worstCaseRegression)

	// --- Bootstrap CI on the generalization gap itself. ---
	//
	// The pooled generalizationGap above (-0.0022) is a single point
	// estimate with no uncertainty attached -- exactly the discipline
	// this project has required since Stage 6 (006-C's own
	// Mann-Whitney correction) for every other quantitative claim, and
	// one this experiment had been skipping for its own headline
	// number. Per-scenario utility (winnerDevPer/baselineDevPer/
	// winnerHoldoutPer/baselineHoldoutPer, already computed above for
	// the robustness summaries) gives a paired improvement value per
	// scenario on each set; the generalization gap is the difference
	// between the two sets' mean paired improvement, so
	// BootstrapDiffCI(devDiffs, holdoutDiffs, MeanStat, ...) resamples
	// exactly that quantity -- the 007-H paired-differences pattern,
	// applied here to a difference-of-differences instead of a single
	// difference.
	devDiffs := make([]float64, len(winnerDevPer))
	for i := range devDiffs {
		devDiffs[i] = winnerDevPer[i] - baselineDevPer[i]
	}
	holdoutDiffs := make([]float64, len(winnerHoldoutPer))
	for i := range holdoutDiffs {
		holdoutDiffs[i] = winnerHoldoutPer[i] - baselineHoldoutPer[i]
	}
	// A dedicated analysis RNG, never an experiment RNG -- see
	// internal/statistics/bootstrap.go's BootstrapCI doc for why reusing
	// one here would risk perturbing evidence a rerun should reproduce
	// exactly.
	analysisRNG := rand.New(rand.NewSource(8_003_001))
	gapCI, err := statistics.BootstrapDiffCI(devDiffs, holdoutDiffs, statistics.MeanStat, 0.95, 5000, analysisRNG)
	if err != nil {
		log.Fatalf("bootstrap CI on generalization gap: %v", err)
	}
	gapCIExcludesZero := gapCI.Lower > 0 || gapCI.Upper < 0
	fmt.Printf("\nGeneralization gap bootstrap 95%% CI (per-scenario paired, Dev minus Holdout improvement): [%.4f, %.4f] (excludes zero: %v)\n",
		gapCI.Lower, gapCI.Upper, gapCIExcludesZero)
	if !gapCIExcludesZero {
		fmt.Println("Note: this interval includes zero -- the pooled gap's SIGN (holdout improvement >= training improvement) is not distinguishable from noise at this scenario-set size. Report the gap as a point estimate consistent with no measurable gap, not as a confirmed negative gap.")
	}

	var tier string
	switch {
	case holdoutImprovement > 0 && generalizationGap < 0.02 && gapCIExcludesZero:
		tier = "strong: improved on both sets, small generalization gap, bootstrap CI excludes zero"
	case holdoutImprovement > 0 && generalizationGap < 0.02:
		tier = "suggestive: improved on both sets with a small pooled generalization gap, but its 95% bootstrap CI includes zero -- not distinguishable from no gap at this scenario-set size"
	case holdoutImprovement > 0:
		tier = "suggestive: improved on holdout, but with a non-trivial generalization gap"
	case holdoutImprovement <= 0 && trainingImprovement > 0:
		tier = "overfit: improved on development only -- do not deploy this candidate over the baseline"
	default:
		tier = "unresolved: no clear improvement on either set"
	}

	// Two provenance/scope notes an adversarial audit surfaced as
	// missing from this experiment's own record, added here rather than
	// only in prose docs so the raw result artifact carries them too.
	const scenarioDistributionNote = "Development and Holdout are drawn from the identical ScenarioSpace " +
		"(same target-count range, service-time range, and failure probability -- see " +
		"internal/tuning/scenario.go's DefaultScenarioSpace), differing only in seed range. This experiment " +
		"tests generalization to unseen SAMPLES from the same generating distribution, not to a distributionally " +
		"different traffic shape; it says nothing about robustness under distribution shift, which is what the " +
		"hand-crafted challenge suite (008-F) probes instead."
	const holdoutTouchNote = "Holdout was scored twice across this stage's lifetime: once under the original " +
		"(p99-based) objective, and again here under the corrected (mean-latency-based) objective after the " +
		"objective function itself was found to be flawed (see docs/learning/008-tuning-final-validation.md). " +
		"The correction was motivated entirely by Development-side evidence (008-F's contradictory ranking and " +
		"the p99/median mismatch found while investigating it) -- Holdout numbers were never inspected or " +
		"compared before the objective was corrected, so this was not a case of adjusting the method after " +
		"seeing a disappointing Holdout result. Recorded explicitly because master context rule 9 asks for any " +
		"re-touching of Holdout to be stated honestly rather than left implicit."

	finding := fmt.Sprintf(
		"008-B's winning candidate (Development utility %.4f, a %.4f improvement over the hand-chosen baseline's "+
			"%.4f) was evaluated against the 20-scenario Holdout set for the first and only time. Its Holdout "+
			"utility was %.4f against the baseline's %.4f Holdout utility -- a holdout improvement of %+.4f, "+
			"against a training improvement of %+.4f, for a generalization gap of %+.4f, whose 95%% bootstrap CI "+
			"(per-scenario paired, 5000 resamples) is [%.4f, %.4f] (excludes zero: %v). Looking past the pooled "+
			"aggregate: the winner's per-scenario utility distribution on Holdout was mean=%.4f/median=%.4f/"+
			"worst=%.4f/stddev=%.4f versus the baseline's mean=%.4f/median=%.4f/worst=%.4f/stddev=%.4f -- a "+
			"worst-case regression of %+.4f. Evidence tier: %s. Scope note: %s Provenance note: %s",
		winner.Utility, trainingImprovement, baselineDevUtility, winnerHoldoutUtility, baselineHoldoutUtility,
		holdoutImprovement, trainingImprovement, generalizationGap,
		gapCI.Lower, gapCI.Upper, gapCIExcludesZero,
		winnerHoldoutRobust.Mean, winnerHoldoutRobust.Median, winnerHoldoutRobust.Worst, winnerHoldoutRobust.StdDev,
		baselineHoldoutRobust.Mean, baselineHoldoutRobust.Median, baselineHoldoutRobust.Worst, baselineHoldoutRobust.StdDev,
		worstCaseRegression, tier, scenarioDistributionNote, holdoutTouchNote,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment                      string                     `json:"experiment"`
		Timestamp                       string                     `json:"timestamp"`
		WinnerConfigHash                string                     `json:"winner_config_hash"`
		WinnerConfig                    proxy.AdaptiveConfig       `json:"winner_config"`
		BaselineConfig                  proxy.AdaptiveConfig       `json:"baseline_config"`
		BaselineDevUtility              float64                    `json:"baseline_dev_utility"`
		WinnerDevUtility                float64                    `json:"winner_dev_utility"`
		BaselineHoldoutUtility          float64                    `json:"baseline_holdout_utility"`
		WinnerHoldoutUtility            float64                    `json:"winner_holdout_utility"`
		BaselineHoldoutMetrics          tuning.Metrics             `json:"baseline_holdout_metrics"`
		WinnerHoldoutMetrics            tuning.Metrics             `json:"winner_holdout_metrics"`
		TrainingImprovement             float64                    `json:"training_improvement"`
		HoldoutImprovement              float64                    `json:"holdout_improvement"`
		GeneralizationGap               float64                    `json:"generalization_gap"`
		BaselineDevRobustness           tuning.RobustnessSummary   `json:"baseline_dev_robustness"`
		WinnerDevRobustness             tuning.RobustnessSummary   `json:"winner_dev_robustness"`
		BaselineHoldoutRobustness       tuning.RobustnessSummary   `json:"baseline_holdout_robustness"`
		WinnerHoldoutRobustness         tuning.RobustnessSummary   `json:"winner_holdout_robustness"`
		WorstCaseRegression             float64                    `json:"worst_case_regression"`
		GeneralizationGapCI             statistics.BootstrapResult `json:"generalization_gap_bootstrap_ci"`
		GeneralizationGapCIExcludesZero bool                       `json:"generalization_gap_ci_excludes_zero"`
		ScenarioDistributionNote        string                     `json:"scenario_distribution_note"`
		HoldoutTouchNote                string                     `json:"holdout_touch_note"`
		EvidenceTier                    string                     `json:"evidence_tier"`
		Findings                        string                     `json:"findings"`
	}{
		Experiment: "008-C-holdout-validation-generalization-gap", Timestamp: time.Now().UTC().Format(time.RFC3339),
		WinnerConfigHash: winner.ConfigHash, WinnerConfig: winner.Config, BaselineConfig: baselineCfg,
		BaselineDevUtility: baselineDevUtility, WinnerDevUtility: winner.Utility,
		BaselineHoldoutUtility: baselineHoldoutUtility, WinnerHoldoutUtility: winnerHoldoutUtility,
		BaselineHoldoutMetrics: baselineHoldoutMetrics, WinnerHoldoutMetrics: winnerHoldoutMetrics,
		TrainingImprovement: trainingImprovement, HoldoutImprovement: holdoutImprovement, GeneralizationGap: generalizationGap,
		BaselineDevRobustness: baselineDevRobust, WinnerDevRobustness: winnerDevRobust,
		BaselineHoldoutRobustness: baselineHoldoutRobust, WinnerHoldoutRobustness: winnerHoldoutRobust,
		WorstCaseRegression: worstCaseRegression, GeneralizationGapCI: gapCI, GeneralizationGapCIExcludesZero: gapCIExcludesZero,
		ScenarioDistributionNote: scenarioDistributionNote, HoldoutTouchNote: holdoutTouchNote,
		EvidenceTier: tier, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "008C-holdout-validation.json"), b, 0644)

	fmt.Println("\nExperiment 008-C complete.")
}
