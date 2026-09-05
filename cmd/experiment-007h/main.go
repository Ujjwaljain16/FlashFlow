package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
)

const outDirName = "experiments/007-adaptive-replay/results"

const (
	slow  = "edge-a-slow"
	fastA = "edge-b-fast"
	fastB = "edge-c-fast"
)

func keyFor(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	return fmt.Sprintf("/cold-%d", i%3)
}

// jitteredScenario builds 007-B's heterogeneous shape (1 slow=100ms, 2
// fast=20ms, 300 requests), but with each arrival's nominal 5ms-spaced
// timestamp perturbed by up to +/-2ms, drawn from a generator seeded by
// this replicate's own seed. This -- not the choice of policy -- is the
// one thing that varies across replicates: both policies in a given
// replicate see the byte-for-byte identical jittered Scenario, so any
// difference between them reflects the policies, not incidental
// per-replicate noise in when requests happened to arrive.
func jitteredScenario(seed int64) replay.Scenario {
	const requests = 300
	const spacing = 5 * time.Millisecond
	rng := rand.New(rand.NewSource(seed))

	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		nominal := spacing.Nanoseconds() * int64(i)
		jitter := (rng.Float64()*4 - 2) * float64(time.Millisecond)
		at := nominal + int64(jitter)
		if at < 0 {
			at = 0
		}
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(at), Key: keyFor(i)}
	}

	return replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: slow, ServiceTime: 100 * time.Millisecond},
			{Name: fastA, ServiceTime: 20 * time.Millisecond},
			{Name: fastB, ServiceTime: 20 * time.Millisecond},
		},
		Arrivals: arrivals,
		Seed:     seed,
	}
}

func slowShare(records []replay.SelectionRecord) float64 {
	if len(records) == 0 {
		return 0
	}
	count := 0
	for _, rec := range records {
		if rec.Target == slow {
			count++
		}
	}
	return float64(count) / float64(len(records))
}

type Replicate struct {
	Seed              int64   `json:"seed"`
	SlowShareLC       float64 `json:"slow_share_lc"`
	SlowShareAdaptive float64 `json:"slow_share_adaptive"`
	Diff              float64 `json:"diff"` // LC - Adaptive; positive means Adaptive avoided the slow target more
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 007-H: Paired Multi-Seed Counterfactual Study")
	fmt.Println(" Is Adaptive's slow-target-avoidance advantage over Least Connections (007-B, a single run)")
	fmt.Println(" robust across independent replicates, or a single-run artifact?")
	fmt.Println("==========================================================================================")

	const nReplicates = 30
	var replicates []Replicate
	var diffs []float64

	for seed := int64(1); seed <= nReplicates; seed++ {
		scenario := jitteredScenario(seed)

		resultLC, err := replay.RunWorld(scenario, replay.LeastConnectionsPolicy())
		if err != nil {
			log.Fatalf("seed %d: LeastConnections RunWorld failed: %v", seed, err)
		}
		resultAdaptive, err := replay.RunWorld(scenario, replay.AdaptivePolicy())
		if err != nil {
			log.Fatalf("seed %d: Adaptive RunWorld failed: %v", seed, err)
		}

		shareLC := slowShare(resultLC.Records)
		shareAdaptive := slowShare(resultAdaptive.Records)
		diff := shareLC - shareAdaptive

		replicates = append(replicates, Replicate{
			Seed: seed, SlowShareLC: shareLC, SlowShareAdaptive: shareAdaptive, Diff: diff,
		})
		diffs = append(diffs, diff)
	}

	meanDiff, err := statistics.Mean(diffs)
	if err != nil {
		log.Fatalf("computing mean diff: %v", err)
	}

	positiveCount := 0
	for _, d := range diffs {
		if d > 0 {
			positiveCount++
		}
	}

	// A dedicated analysis RNG, never the experiment's own -- reusing a
	// seed that generated the evidence being analyzed would let analysis
	// silently perturb what a later rerun of this same experiment
	// produces. See internal/statistics/bootstrap.go's BootstrapCI doc.
	analysisRNG := rand.New(rand.NewSource(1_000_003))
	ci, err := statistics.BootstrapCI(diffs, statistics.MeanStat, 0.95, 2000, analysisRNG)
	if err != nil {
		log.Fatalf("bootstrap CI failed: %v", err)
	}

	fmt.Printf("\n%d paired replicates (each: identical jittered Scenario, Least Connections vs Adaptive)\n", nReplicates)
	fmt.Printf("Mean paired diff (LC slow-share - Adaptive slow-share): %.4f\n", meanDiff)
	fmt.Printf("95%% bootstrap CI on the mean diff: [%.4f, %.4f]\n", ci.Lower, ci.Upper)
	fmt.Printf("Sign consistency: %d/%d replicates favored Adaptive (diff > 0)\n", positiveCount, nReplicates)

	var tier string
	switch {
	case ci.Lower > 0:
		tier = "strong"
	case ci.Upper < 0:
		tier = "strong (opposite direction from what was expected)"
	default:
		tier = "unresolved"
	}
	fmt.Printf("Evidence tier: %s\n", tier)

	finding := fmt.Sprintf(
		"The statistical unit here is one full scenario replicate (a complete 300-request run), not one request "+
			"-- 30 independent replicates, each varying only in its arrivals' timing jitter (+/-2ms around the "+
			"nominal 5ms grid, seeded per replicate), with Least Connections and Adaptive run against the byte-for-"+
			"byte identical jittered Scenario within each replicate (a paired design, not two independent samples). "+
			"The paired difference (LC's slow-target share minus Adaptive's) was bootstrapped directly on the 30 "+
			"per-replicate differences -- BootstrapCI on the differences themselves, not BootstrapDiffCI on the two "+
			"policies' raw shares treated as unpaired samples, because the pairing is exactly what should be "+
			"exploited here: LC_i and Adaptive_i share the identical arrival pattern, so their difference cancels "+
			"whatever that specific replicate's jitter contributed, leaving only the two policies' own behavioral "+
			"gap. Mean paired diff: %.4f (95%% CI [%.4f, %.4f]), with %d/%d replicates favoring Adaptive. "+
			"Evidence tier: %s. This extends 007-B's single-run finding (Adaptive 5.7%% vs Least Connections 14.0%% "+
			"slow-target share) into a claim about robustness across independently-varying conditions, not just one "+
			"observed run -- exactly the distinction Stage 6 established as necessary before treating a single "+
			"experiment's result as more than a starting hypothesis.",
		meanDiff, ci.Lower, ci.Upper, positiveCount, nReplicates, tier,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment    string                     `json:"experiment"`
		Timestamp     string                     `json:"timestamp"`
		NReplicates   int                        `json:"n_replicates"`
		Replicates    []Replicate                `json:"replicates"`
		MeanDiff      float64                    `json:"mean_diff"`
		BootstrapCI   statistics.BootstrapResult `json:"bootstrap_ci"`
		PositiveCount int                        `json:"positive_count"`
		EvidenceTier  string                     `json:"evidence_tier"`
		Findings      string                     `json:"findings"`
	}{
		Experiment: "007-H-paired-multiseed-counterfactual", Timestamp: time.Now().UTC().Format(time.RFC3339),
		NReplicates: nReplicates, Replicates: replicates, MeanDiff: meanDiff,
		BootstrapCI: ci, PositiveCount: positiveCount, EvidenceTier: tier, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "007H-paired-multiseed-counterfactual.json"), b, 0644)

	fmt.Println("\nExperiment 007-H complete.")
}
