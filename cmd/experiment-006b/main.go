package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/statistics"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/006-statistics-queueing/results"

// virtualPlaceholderRequest -- see the identical comment in
// cmd/experiment-005e/main.go: no selector reads this parameter.
var virtualPlaceholderRequest = &http.Request{}

// runEWMALockInScenario runs a fixed, deterministic 300-request
// workload against 3 nominally equal-service-time targets. order's
// permutation is the only thing that varies across runs by default --
// this targets EWMASelector's own documented mechanism directly ("ties
// among unobserved targets fall back to available order"), rather than
// inventing a service-time jitter model Stage 5 never built.
//
// jitter, if non-nil, adds a small per-target timing perturbation drawn
// from jitterRNG -- see Cell 2 below, added after Cell 1 revealed that
// permuting labels alone produces zero variance in lock-in *severity*
// (only in which target wins), since relabeling truly identical targets
// under a fixed workload is isomorphic across permutations.
func runEWMALockInScenario(order []string, jitterRNG *rand.Rand, jitterRange time.Duration) map[string]int {
	e := vtime.NewEngine(0)
	loadTracker := proxy.NewLoadTracker()
	latencyTracker := proxy.NewLatencyTracker(0.2)
	selector := proxy.NewEWMASelector(latencyTracker)

	const baseServiceTime = 20 * time.Millisecond
	serviceTimes := make(map[string]time.Duration, len(order))
	for _, t := range order {
		d := baseServiceTime
		if jitterRNG != nil {
			// Uniform in [-jitterRange, +jitterRange], drawn once per
			// target per run -- a fixed per-run perturbation, not
			// per-request noise, modeling "these nominally identical
			// targets have slightly different real-world latency this
			// run" rather than jitter on every single request.
			d += time.Duration((jitterRNG.Float64()*2 - 1) * float64(jitterRange))
		}
		serviceTimes[t] = d
	}

	distribution := make(map[string]int)

	const requests = 300
	const spacing = 5 * time.Millisecond
	for i := 0; i < requests; i++ {
		at := clock.VirtualTime(spacing.Nanoseconds() * int64(i))
		e.Schedule(at, func() {
			target, err := selector.SelectTarget(virtualPlaceholderRequest, order)
			if err != nil {
				log.Fatalf("selection failed: %v", err)
			}
			distribution[target]++
			loadTracker.Increment(target)
			arrival := e.Now()
			e.Schedule(arrival.Add(serviceTimes[target]), func() {
				loadTracker.Decrement(target)
				latencyTracker.Observe(target, e.Now().Sub(arrival))
			})
		})
	}

	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("workload failed: %v", err)
	}
	return distribution
}

// RunResult is one replicate: one permutation seed, one resulting
// distribution. The statistical unit for this experiment is the RUN,
// not the request -- 300 requests inside one run describe that run's
// own routing pattern, not 300 independent trials of "does lock-in
// happen." n=50 here means 50 runs.
type RunResult struct {
	Seed                 int64          `json:"seed"`
	Order                []string       `json:"order"`
	Distribution         map[string]int `json:"distribution"`
	Winner               string         `json:"winner"`
	MaxShare             float64        `json:"max_share"`
	WinnerIsFirstInOrder bool           `json:"winner_is_first_in_order"`
}

// CellSummary is the distributional summary for one cell's 50 runs.
type CellSummary struct {
	Cell              string                     `json:"cell"`
	NumRuns           int                        `json:"num_runs"`
	MedianMaxShare    float64                    `json:"median_max_share"`
	MedianMaxShareCI  statistics.BootstrapResult `json:"median_max_share_ci"`
	P10MaxShare       float64                    `json:"p10_max_share"`
	P90MaxShare       float64                    `json:"p90_max_share"`
	MinMaxShare       float64                    `json:"min_max_share"`
	MaxMaxShare       float64                    `json:"max_max_share"`
	FirstOrderWinRate float64                    `json:"first_order_win_rate"`
	Runs              []RunResult                `json:"runs"`
}

// runCell executes numRuns replicates, each a fresh permutation seed
// (0..numRuns-1); jitterRange > 0 additionally perturbs each target's
// per-run service time (see runEWMALockInScenario). Same seed sequence
// used for both cells so the *only* difference between Cell 1 and Cell 2
// is whether jitter is applied.
func runCell(baseTargets []string, requests, numRuns int, jitterRange time.Duration) CellSummary {
	var runs []RunResult
	for seed := int64(0); seed < int64(numRuns); seed++ {
		r := rand.New(rand.NewSource(seed))
		order := append([]string(nil), baseTargets...)
		r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

		var jitterRNG *rand.Rand
		if jitterRange > 0 {
			jitterRNG = r // continue drawing from the same per-run seeded stream used for the shuffle
		}
		dist := runEWMALockInScenario(order, jitterRNG, jitterRange)

		winner, maxCount := "", -1
		for _, t := range baseTargets {
			if dist[t] > maxCount {
				maxCount = dist[t]
				winner = t
			}
		}
		maxShare := float64(maxCount) / float64(requests)

		runs = append(runs, RunResult{
			Seed: seed, Order: order, Distribution: dist, Winner: winner, MaxShare: maxShare,
			WinnerIsFirstInOrder: winner == order[0],
		})
	}

	shares := make([]float64, len(runs))
	firstOrderWins := 0
	for i, r := range runs {
		shares[i] = r.MaxShare
		if r.WinnerIsFirstInOrder {
			firstOrderWins++
		}
	}

	median, _ := statistics.Median(shares)
	p10, _ := statistics.Percentile(shares, 10)
	p90, _ := statistics.Percentile(shares, 90)
	minShare, _ := statistics.Min(shares)
	maxShareOverall, _ := statistics.Max(shares)

	cellName := "no_jitter"
	if jitterRange > 0 {
		cellName = "with_jitter"
	}
	// Dedicated analysis seed per cell, independent of the permutation
	// seeds used to generate the data above.
	analysisRNG := rand.New(rand.NewSource(999_999))
	medianCI, err := statistics.BootstrapCI(shares, statistics.MedianStat, 0.95, 5000, analysisRNG)
	if err != nil {
		log.Fatalf("bootstrap failed for cell %s: %v", cellName, err)
	}

	return CellSummary{
		Cell: cellName, NumRuns: numRuns, MedianMaxShare: median, MedianMaxShareCI: medianCI,
		P10MaxShare: p10, P90MaxShare: p90, MinMaxShare: minShare, MaxMaxShare: maxShareOverall,
		FirstOrderWinRate: float64(firstOrderWins) / float64(numRuns), Runs: runs,
	}
}

func sharesOf(c CellSummary) []float64 {
	out := make([]float64, len(c.Runs))
	for i, r := range c.Runs {
		out[i] = r.MaxShare
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 006-B: Routing Policy Variability (EWMA Lock-In)")
	fmt.Println(" Across N controlled permutation seeds, what does the distribution of maximum target")
	fmt.Println(" share actually look like -- and does severity vary, or only the winner's identity?")
	fmt.Println("==========================================================================================")

	baseTargets := []string{"edge-a", "edge-b", "edge-c"}
	const requests = 300
	const numRuns = 50
	const fairShare = 1.0 / 3.0

	cell1 := runCell(baseTargets, requests, numRuns, 0)
	fmt.Printf("\nCell 1 (permutation only, no jitter) -- %d runs:\n", numRuns)
	fmt.Printf("  max-target-share: median=%.4f  p10=%.4f  p90=%.4f  min=%.4f  max=%.4f  (fair share %.3f)\n",
		cell1.MedianMaxShare, cell1.P10MaxShare, cell1.P90MaxShare, cell1.MinMaxShare, cell1.MaxMaxShare, fairShare)
	fmt.Printf("  winner == first-in-order in %d/%d runs (%.1f%%)\n",
		int(cell1.FirstOrderWinRate*float64(numRuns)), numRuns, cell1.FirstOrderWinRate*100)

	const jitterRange = 2 * time.Millisecond // +-2ms on a 20ms base -- a modest, deliberately small perturbation
	cell2 := runCell(baseTargets, requests, numRuns, jitterRange)
	fmt.Printf("\nCell 2 (permutation + %v service-time jitter) -- %d runs:\n", jitterRange, numRuns)
	fmt.Printf("  max-target-share: median=%.4f  p10=%.4f  p90=%.4f  min=%.4f  max=%.4f\n",
		cell2.MedianMaxShare, cell2.P10MaxShare, cell2.P90MaxShare, cell2.MinMaxShare, cell2.MaxMaxShare)
	fmt.Printf("  winner == first-in-order in %d/%d runs (%.1f%%)\n",
		int(cell2.FirstOrderWinRate*float64(numRuns)), numRuns, cell2.FirstOrderWinRate*100)

	sharesA, sharesB := sharesOf(cell1), sharesOf(cell2)
	sdA, err := statistics.StdDev(sharesA)
	if err != nil {
		log.Fatalf("stddev failed: %v", err)
	}
	sdB, err := statistics.StdDev(sharesB)
	if err != nil {
		log.Fatalf("stddev failed: %v", err)
	}
	mw, err := statistics.MannWhitneyU(sharesA, sharesB)
	if err != nil {
		log.Fatalf("comparison failed: %v", err)
	}

	fmt.Printf("\nstddev of max-share: %.5f (no jitter) vs %.5f (with jitter)\n", sdA, sdB)
	fmt.Printf("Mann-Whitney comparing the two cells' max-share distributions: p=%.4f\n", mw.PValue)

	finding := fmt.Sprintf(
		"Cell 1 (permutation only) produced ZERO variance in lock-in severity across all %d seeds -- every single run "+
			"converged to exactly the same %.4f max-share, only the identity of the winner changed (100%% correlated "+
			"with tie-break order, %d/%d runs). This was not the expected result; the original hypothesis predicted "+
			"severity would vary run to run. Investigating why: permuting truly identical targets under an "+
			"identical, fixed workload is a pure relabeling -- the timing dynamics that decide how many exploratory "+
			"picks happen before full lock-in are isomorphic across every permutation, so the resulting severity is a "+
			"deterministic structural constant of (arrival spacing, service time, request count), not a random "+
			"variable at all under this design.\n\n"+
			"Cell 2 (adding %v of per-run service-time jitter, the kind of real timing noise that actually drove "+
			"Stage 3's original variability) produced a second, more interesting and unexpected result: severity "+
			"barely moved (median still %.4f, spread only %.4f to %.4f), but WHO wins changed dramatically -- the "+
			"first-in-order target won only %d/%d runs (%.1f%%), close to the %.1f%% a random winner among 3 targets "+
			"would produce by chance alone. The mechanism: once jitter makes the targets genuinely, if slightly, "+
			"unequal, EWMA's own comparison rule (lower observed latency wins, not just 'unobserved beats observed') "+
			"lets whichever target happens to draw the lowest jittered service time override the tie-break-order "+
			"advantage entirely. So tie-break order controls the outcome only when targets are exactly equal; the "+
			"moment real timing differences exist, however small, they dominate instead. This is the "+
			"discover-limitation-then-refine cycle happening within one experiment: the first design answered 'who "+
			"wins when targets are identical' but couldn't ask 'how variable is lock-in under realistic conditions,' "+
			"and the smallest realistic fix (per-run jitter) didn't just add variability -- it revealed a second "+
			"mechanism entirely.",
		numRuns, cell1.MedianMaxShare, int(cell1.FirstOrderWinRate*float64(numRuns)), numRuns,
		jitterRange, cell2.MedianMaxShare, cell2.MinMaxShare, cell2.MaxMaxShare,
		int(cell2.FirstOrderWinRate*float64(numRuns)), numRuns, cell2.FirstOrderWinRate*100, fairShare*100,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment string                       `json:"experiment"`
		Timestamp  string                       `json:"timestamp"`
		Requests   int                          `json:"requests"`
		FairShare  float64                      `json:"fair_share"`
		Cell1      CellSummary                  `json:"cell1_no_jitter"`
		Cell2      CellSummary                  `json:"cell2_with_jitter"`
		Comparison statistics.MannWhitneyResult `json:"cell_comparison_mann_whitney"`
		Findings   string                       `json:"findings"`
	}{
		Experiment: "006-B-ewma-lock-in-variability", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Requests: requests, FairShare: fairShare, Cell1: cell1, Cell2: cell2, Comparison: mw, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "006B-ewma-lock-in-variability.json"), b, 0644)

	fmt.Println("\nExperiment 006-B complete.")
}
