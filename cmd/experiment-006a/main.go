package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/statistics"
)

const outDirName = "experiments/006-statistics-queueing/results"

// dataSeed generates synthetic samples; analysisSeed drives bootstrap
// resampling. Kept deliberately separate per internal/statistics'
// bootstrap.go doc comment: an analysis must never share a generator
// with the thing it's analyzing.
const dataSeed = 1
const analysisSeed = 2001

func normalSample(r *rand.Rand, n int, mean, stddev float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = mean + r.NormFloat64()*stddev
	}
	return out
}

type ScenarioResult struct {
	Name     string `json:"name"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail"`
	Findings string `json:"findings"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 006-A: Statistical Validation")
	fmt.Println(" Do the implemented statistical methods behave correctly on known synthetic datasets?")
	fmt.Println("==========================================================================================")

	dataRNG := rand.New(rand.NewSource(dataSeed))
	var results []ScenarioResult
	allPass := true

	record := func(r ScenarioResult) {
		results = append(results, r)
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
			allPass = false
		}
		fmt.Printf("\n[%s] %s\n  %s\n", status, r.Name, r.Detail)
	}

	// Scenario (a): identical distributions -- no meaningful difference expected.
	{
		a := normalSample(dataRNG, 200, 100, 10)
		b := normalSample(dataRNG, 200, 100, 10)
		mw, err := statistics.MannWhitneyU(a, b)
		if err != nil {
			log.Fatalf("scenario a: %v", err)
		}
		cd, err := statistics.CliffsDelta(a, b)
		if err != nil {
			log.Fatalf("scenario a: %v", err)
		}
		pass := mw.PValue > 0.05 && cd.Magnitude == "negligible"
		record(ScenarioResult{
			Name: "identical_distributions", Pass: pass,
			Detail:   fmt.Sprintf("p=%.4f (want >0.05), cliffs_delta=%.4f magnitude=%s (want negligible)", mw.PValue, cd.Delta, cd.Magnitude),
			Findings: "Two samples from the same normal(100,10) should show no evidence of a shift and a negligible effect size.",
		})
	}

	// Scenario (b): clearly shifted distributions -- a 3-sigma shift should
	// be unambiguous.
	{
		a := normalSample(dataRNG, 200, 100, 10)
		b := normalSample(dataRNG, 200, 130, 10)
		mw, err := statistics.MannWhitneyU(a, b)
		if err != nil {
			log.Fatalf("scenario b: %v", err)
		}
		cd, err := statistics.CliffsDelta(a, b)
		if err != nil {
			log.Fatalf("scenario b: %v", err)
		}
		pass := mw.PValue < 0.001 && cd.Magnitude == "large"
		record(ScenarioResult{
			Name: "clearly_shifted_distributions", Pass: pass,
			Detail:   fmt.Sprintf("p=%.6f (want <0.001), cliffs_delta=%.4f magnitude=%s (want large)", mw.PValue, cd.Delta, cd.Magnitude),
			Findings: "A 3-sigma shift (normal(100,10) vs normal(130,10)) should be detected with a small p-value and a large effect size.",
		})
	}

	// Scenario (c): same practical effect at two sample sizes -- effect
	// size should stay similar, precision (bootstrap CI width) and power
	// (p-value) should improve with n.
	{
		smallA := normalSample(dataRNG, 20, 100, 10)
		smallB := normalSample(dataRNG, 20, 105, 10)
		largeA := normalSample(dataRNG, 400, 100, 10)
		largeB := normalSample(dataRNG, 400, 105, 10)

		cdSmall, _ := statistics.CliffsDelta(smallA, smallB)
		cdLarge, _ := statistics.CliffsDelta(largeA, largeB)

		analysisRNG := rand.New(rand.NewSource(analysisSeed))
		ciSmall, err := statistics.BootstrapDiffCI(smallA, smallB, statistics.MeanStat, 0.95, 2000, analysisRNG)
		if err != nil {
			log.Fatalf("scenario c: %v", err)
		}
		analysisRNG2 := rand.New(rand.NewSource(analysisSeed))
		ciLarge, err := statistics.BootstrapDiffCI(largeA, largeB, statistics.MeanStat, 0.95, 2000, analysisRNG2)
		if err != nil {
			log.Fatalf("scenario c: %v", err)
		}

		widthSmall := ciSmall.Upper - ciSmall.Lower
		widthLarge := ciLarge.Upper - ciLarge.Lower
		effectSizesSimilar := absf(cdSmall.Delta-cdLarge.Delta) < 0.25 // generous tolerance -- both are noisy estimates of the same true effect
		precisionImproved := widthLarge < widthSmall

		pass := effectSizesSimilar && precisionImproved
		record(ScenarioResult{
			Name: "same_effect_different_sample_size", Pass: pass,
			Detail: fmt.Sprintf(
				"n=20: cliffs_delta=%.3f CI_width=%.3f | n=400: cliffs_delta=%.3f CI_width=%.3f (want similar delta, narrower CI at n=400)",
				cdSmall.Delta, widthSmall, cdLarge.Delta, widthLarge),
			Findings: "The same underlying 5-unit mean shift should produce a similar Cliff's Delta at n=20 and n=400, but a visibly narrower bootstrap CI at n=400 -- sample size buys precision, not a bigger effect.",
		})
	}

	// Scenario (d): outlier-heavy distribution -- Cliff's Delta (rank-based)
	// should be far less disturbed by a few extreme values than a raw mean
	// difference would be.
	{
		clean := normalSample(dataRNG, 100, 100, 10)
		contaminated := append([]float64(nil), clean...)
		// Inject 3 extreme outliers (out of 100 points) far above the rest.
		contaminated[0] = 100000
		contaminated[1] = 150000
		contaminated[2] = 200000

		meanClean, _ := statistics.Mean(clean)
		meanContaminated, _ := statistics.Mean(contaminated)
		medianClean, _ := statistics.Median(clean)
		medianContaminated, _ := statistics.Median(contaminated)
		cd, err := statistics.CliffsDelta(clean, contaminated)
		if err != nil {
			log.Fatalf("scenario d: %v", err)
		}

		meanShift := absf(meanContaminated - meanClean)
		medianShift := absf(medianContaminated - medianClean)
		// The mean shift from 3 outliers should be enormous; the median
		// shift should be tiny; Cliff's Delta should reflect that clean
		// and contaminated are still overwhelmingly similar distributions
		// (only 3/100 points actually differ), not a "large" effect.
		pass := meanShift > 1000 && medianShift < 1 && cd.Magnitude != "large"
		record(ScenarioResult{
			Name: "outlier_heavy_distribution", Pass: pass,
			Detail: fmt.Sprintf(
				"mean shift=%.1f (want >1000, outlier-driven), median shift=%.3f (want <1, outlier-robust), cliffs_delta=%.4f magnitude=%s (want not large)",
				meanShift, medianShift, cd.Delta, cd.Magnitude),
			Findings: "Injecting 3 extreme outliers into a 100-point sample should distort the mean drastically while leaving the median and the rank-based effect size largely unaffected -- exactly why this project prefers percentile/rank statistics for latency data.",
		})
	}

	// Scenario (e): known bootstrap case -- a large sample from Uniform(0,100)
	// has a true mean of exactly 50; the bootstrap CI should bracket it.
	{
		n := 2000
		uniform := make([]float64, n)
		for i := range uniform {
			uniform[i] = dataRNG.Float64() * 100
		}
		analysisRNG := rand.New(rand.NewSource(analysisSeed))
		ci, err := statistics.BootstrapCI(uniform, statistics.MeanStat, 0.95, 5000, analysisRNG)
		if err != nil {
			log.Fatalf("scenario e: %v", err)
		}
		trueMean := 50.0
		pass := ci.Lower <= trueMean && trueMean <= ci.Upper
		record(ScenarioResult{
			Name: "known_bootstrap_case", Pass: pass,
			Detail:   fmt.Sprintf("95%% CI=[%.3f, %.3f] for true mean=%.1f (want true mean inside the interval)", ci.Lower, ci.Upper, trueMean),
			Findings: "A 2000-point sample from Uniform(0,100) has a true mean of exactly 50; the bootstrap CI for the sample mean should contain it.",
		})
	}

	// Determinism check: same analysis seed on the same data must
	// reproduce the exact same bootstrap CI (item 54).
	{
		sample := normalSample(rand.New(rand.NewSource(99)), 100, 100, 10)
		r1, _ := statistics.BootstrapCI(sample, statistics.MeanStat, 0.95, 2000, rand.New(rand.NewSource(analysisSeed)))
		r2, _ := statistics.BootstrapCI(sample, statistics.MeanStat, 0.95, 2000, rand.New(rand.NewSource(analysisSeed)))
		pass := r1 == r2
		record(ScenarioResult{
			Name: "deterministic_analysis_seed", Pass: pass,
			Detail:   fmt.Sprintf("run1=%+v run2=%+v (want identical)", r1, r2),
			Findings: "Same data + same analysis seed must produce the exact same bootstrap confidence interval, every time.",
		})
	}

	fmt.Printf("\n--- Summary: %d/%d scenarios passed ---\n", countPass(results), len(results))

	out := struct {
		Experiment string           `json:"experiment"`
		Timestamp  string           `json:"timestamp"`
		AllPass    bool             `json:"all_pass"`
		Scenarios  []ScenarioResult `json:"scenarios"`
	}{
		Experiment: "006-A-statistical-validation", Timestamp: time.Now().UTC().Format(time.RFC3339),
		AllPass: allPass, Scenarios: results,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "006A-statistical-validation.json"), b, 0644)

	if !allPass {
		log.Fatal("experiment 006-A failed: one or more validation scenarios did not behave as expected")
	}

	fmt.Println("\nExperiment 006-A complete.")
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func countPass(results []ScenarioResult) int {
	n := 0
	for _, r := range results {
		if r.Pass {
			n++
		}
	}
	return n
}
