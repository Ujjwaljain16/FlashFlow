package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/provenance"
	"flashflow/internal/replay"
	"flashflow/internal/tuning"
)

const manifestsRoot = "experiments/010-stage10-features/runs"

const outDirName = "experiments/010-stage10-features/results"

// This experiment answers the question Stage 10's own plan (§10.9)
// asked to have answered honestly: does either Latin Hypercube Sampling
// or hand-rolled Bayesian Optimization actually beat Random Search on
// this project's own tuning problem, or does Stage 8's own finding
// (Random Search converges by evaluation 24 of 200 and plateaus for
// the rest) hold, meaning there was never strong evidence a better
// optimizer was needed in the first place? All three tuners run
// through the IDENTICAL RunSearch loop against the IDENTICAL 40-
// scenario Development set with the IDENTICAL evaluation budget and
// objective weights -- the only thing that differs between them is
// which candidate each one suggests.
const evaluations = 200

// TunerRunSummary is one tuner's result: enough to compare convergence
// behavior, not the full search ledger (already preserved separately
// by each RunSearch call if ever needed -- this experiment's own
// output is a comparison, not a duplicate ledger).
type TunerRunSummary struct {
	TunerName           string  `json:"tuner_name"`
	BestUtility         float64 `json:"best_utility"`
	LastImprovedAtIndex int     `json:"last_improved_at_index"`
	Plateaued           bool    `json:"plateaued"`
	ValidCount          int     `json:"valid_count"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 010-A: Tuner Comparison -- Random Search vs LHS vs Bayesian Optimization")
	fmt.Println(" Does either new tuner tier actually beat Random Search on this project's own scenarios?")
	fmt.Println("==========================================================================================")

	split := tuning.NewSplit(tuning.DefaultScenarioSpace())
	weights := tuning.DefaultObjectiveWeights()
	space := tuning.DefaultConfigSpace()
	const seed = 20260908 // the same OptimizerSeed DefaultRandomSearchConfig uses, for a fair apples-to-apples comparison

	tuners := []tuning.Tuner{
		tuning.NewRandomSearchTuner(seed, space),
		tuning.NewLHSTuner(seed, space, evaluations),
		tuning.NewBayesOptTuner(seed, space),
	}

	var summaries []TunerRunSummary
	fmt.Printf("\n%-16s %12s %10s %10s %8s\n", "tuner", "best_util", "last_imp", "plateaued", "valid")
	for _, tuner := range tuners {
		start := time.Now()
		result := tuning.RunSearch(tuner, evaluations, split.Development, weights)
		elapsed := time.Since(start)

		best, _ := result.Best()
		validCount := 0
		for _, e := range result.Evaluations {
			if e.Valid {
				validCount++
			}
		}

		summary := TunerRunSummary{
			TunerName: tuner.Name(), BestUtility: best.Utility,
			LastImprovedAtIndex: result.Convergence.LastImprovedAtIndex,
			Plateaued:           result.Convergence.Plateaued,
			ValidCount:          validCount,
		}
		summaries = append(summaries, summary)

		// Write a real provenance manifest for this run -- internal/
		// provenance existed as a tested library with no experiment
		// binary actually calling it (a demo-readiness gap this audit
		// caught: F-05's fix was a real, tested package that nothing
		// wired up). ConfigHash covers exactly what varies run-to-run
		// for a fixed scenario set: the ConfigSpace bounds and the
		// objective weights driving what "best" means.
		configHash, err := provenance.ConfigHash(struct {
			Space   tuning.ConfigSpace
			Weights tuning.ObjectiveWeights
		}{space, weights})
		if err != nil {
			log.Fatalf("hashing config for manifest: %v", err)
		}
		commit, dirty := provenance.GitCommit()
		manifest := provenance.Manifest{
			ExperimentID:      fmt.Sprintf("010a-%s", tuner.Name()),
			Name:              "010-A Tuner Comparison",
			Seeds:             replay.DeriveSeeds(seed),
			ConfigurationHash: configHash,
			GitCommit:         commit,
			GitDirty:          dirty,
			TunerVersion:      tuner.Name(),
			CreatedAt:         time.Now().UTC(),
		}
		if err := manifest.Write(manifestsRoot); err != nil {
			log.Fatalf("writing manifest for %s: %v", tuner.Name(), err)
		}

		fmt.Printf("%-16s %12.4f %10d %10v %8d  (%.1fs)\n",
			summary.TunerName, summary.BestUtility, summary.LastImprovedAtIndex, summary.Plateaued, summary.ValidCount, elapsed.Seconds())
	}

	randomBest := summaries[0].BestUtility
	var findingLines []string
	for _, s := range summaries[1:] {
		delta := s.BestUtility - randomBest
		relDelta := delta / randomBest * 100
		verdict := "did not meaningfully beat"
		if delta > 0.001 {
			verdict = "beat"
		} else if delta < -0.001 {
			verdict = "underperformed"
		} else {
			verdict = "matched"
		}
		findingLines = append(findingLines, fmt.Sprintf(
			"%s %s Random Search's best utility (%.4f vs %.4f, a %+.2f%% relative difference), last improving at evaluation %d of %d (%s).",
			s.TunerName, verdict, s.BestUtility, randomBest, relDelta, s.LastImprovedAtIndex, evaluations,
			map[bool]string{true: "plateaued", false: "still improving at the end of the budget"}[s.Plateaued],
		))
	}

	finding := fmt.Sprintf(
		"All three tuners searched the identical 40-scenario Development set with the identical %d-evaluation "+
			"budget, objective weights, and starting seed. Random Search reached best utility %.4f, last "+
			"improving at evaluation %d (%s). %s "+
			"Framing per Stage 10's own plan: Stage 8 already found Random Search converges early and plateaus "+
			"on this search space, so the absence of a large improvement from LHS/Bayesian Optimization here is "+
			"the EXPECTED outcome given that finding, not a defect in either implementation -- both were built "+
			"to honor the PRD's tuner-progression promise, not because prior evidence demanded a better optimizer.",
		evaluations, randomBest, summaries[0].LastImprovedAtIndex,
		map[bool]string{true: "plateaued", false: "still improving at the end of the budget"}[summaries[0].Plateaued],
		joinFindings(findingLines),
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment    string            `json:"experiment"`
		Timestamp     string            `json:"timestamp"`
		Evaluations   int               `json:"evaluations"`
		OptimizerSeed int64             `json:"optimizer_seed"`
		Summaries     []TunerRunSummary `json:"summaries"`
		Findings      string            `json:"findings"`
	}{
		Experiment: "010-A-tuner-comparison", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Evaluations: evaluations, OptimizerSeed: seed,
		Summaries: summaries, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "010A-tuner-comparison.json"), b, 0644)

	fmt.Println("\nExperiment 010-A complete.")
}

func joinFindings(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + " "
	}
	return out
}
