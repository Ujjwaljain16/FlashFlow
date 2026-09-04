package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

// virtualPlaceholderRequest -- see the identical comment in
// cmd/experiment-005e/main.go: no selector reads this parameter.
var virtualPlaceholderRequest = &http.Request{}

type targetProfile struct {
	name        string
	serviceTime time.Duration
}

// runP2CWorkload fires n requests at a fixed schedule against a fresh
// P2C-over-load selector seeded with seed, returning the exact ordered
// sequence of routing decisions -- not just the final distribution --
// since seed reproducibility is a claim about the whole decision
// sequence, not merely its aggregate shape.
func runP2CWorkload(seed int64, n int, arrivalSpacing time.Duration, profiles []targetProfile) []string {
	e := vtime.NewEngine(0)
	loadTracker := proxy.NewLoadTracker()
	selector := proxy.NewP2CSelector(proxy.ScorerFromLoad(loadTracker), rand.New(rand.NewSource(seed)))

	targets := make([]string, len(profiles))
	serviceTimes := make(map[string]time.Duration, len(profiles))
	for i, p := range profiles {
		targets[i] = p.name
		serviceTimes[p.name] = p.serviceTime
	}

	var decisions []string
	for i := 0; i < n; i++ {
		at := clock.VirtualTime(arrivalSpacing.Nanoseconds() * int64(i))
		e.Schedule(at, func() {
			target, err := selector.SelectTarget(virtualPlaceholderRequest, targets)
			if err != nil {
				log.Fatalf("selection failed: %v", err)
			}
			decisions = append(decisions, target)
			loadTracker.Increment(target)
			svc := serviceTimes[target]
			e.Schedule(e.Now().Add(svc), func() {
				loadTracker.Decrement(target)
			})
		})
	}

	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("workload failed: %v", err)
	}
	return decisions
}

func summarize(decisions []string) map[string]int {
	dist := make(map[string]int)
	for _, d := range decisions {
		dist[d]++
	}
	return dist
}

type Experiment005FResult struct {
	Experiment           string         `json:"experiment"`
	Timestamp            string         `json:"timestamp"`
	Requests             int            `json:"requests"`
	SeedA                int64          `json:"seed_a"`
	SeedBSameAsA         int64          `json:"seed_b_same_as_a"`
	SeedCDifferent       int64          `json:"seed_c_different"`
	RunAEqualsRunB       bool           `json:"run_a_equals_run_b"`
	RunAEqualsRunC       bool           `json:"run_a_equals_run_c"`
	FirstDivergenceIndex int            `json:"first_divergence_index"`
	DistributionA        map[string]int `json:"distribution_a"`
	DistributionC        map[string]int `json:"distribution_c"`
	Findings             string         `json:"findings"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-F: Seeded Randomness")
	fmt.Println(" Can a randomized routing policy be reproduced exactly under the same seed?")
	fmt.Println("==========================================================================================")

	// All three targets identically fast -- isolates P2C's own random pair
	// sampling as the only meaningful source of variation, rather than
	// conflating it with the heterogeneous-capacity dynamics 005-E already
	// covered.
	profiles := []targetProfile{
		{"edge-a", 20 * time.Millisecond},
		{"edge-b", 20 * time.Millisecond},
		{"edge-c", 20 * time.Millisecond},
	}
	const requests = 300
	const spacing = 5 * time.Millisecond
	const seedA int64 = 1
	const seedBSameAsA int64 = 1
	const seedCDifferent int64 = 2

	runA := runP2CWorkload(seedA, requests, spacing, profiles)
	runB := runP2CWorkload(seedBSameAsA, requests, spacing, profiles)
	runC := runP2CWorkload(seedCDifferent, requests, spacing, profiles)

	sameSeedIdentical := reflect.DeepEqual(runA, runB)
	diffSeedIdentical := reflect.DeepEqual(runA, runC)

	firstDivergence := -1
	for i := 0; i < len(runA) && i < len(runC); i++ {
		if runA[i] != runC[i] {
			firstDivergence = i
			break
		}
	}

	distA := summarize(runA)
	distC := summarize(runC)

	fmt.Printf("\nSeed %d, run 1: %v\n", seedA, distA)
	fmt.Printf("Seed %d, run 2 (same seed): identical to run 1 = %v\n", seedBSameAsA, sameSeedIdentical)
	fmt.Printf("Seed %d (different): %v\n", seedCDifferent, distC)
	fmt.Printf("Same as seed %d's decision sequence: %v (first divergence at request #%d)\n", seedA, diffSeedIdentical, firstDivergence)

	var finding string
	switch {
	case !sameSeedIdentical:
		finding = "DETERMINISM VIOLATED: two runs with the identical seed produced different decision sequences."
	case diffSeedIdentical:
		finding = fmt.Sprintf(
			"Same seed reproduced the identical %d-decision sequence exactly, as expected. A different seed happened to "+
				"also produce the same sequence this time -- possible but not guaranteed by the design; it does not "+
				"indicate a problem, since nothing requires two different seeds to diverge on every workload.",
			requests,
		)
	default:
		finding = fmt.Sprintf(
			"Same seed (%d) reproduced the identical %d-decision sequence exactly across two independent runs. A "+
				"different seed (%d) diverged from it starting at request #%d, while the workload itself -- arrival "+
				"times, target set, service times -- was identical in all three runs and never varied. Randomness is "+
				"controlled by the explicit seed alone, not by goroutine scheduling, map iteration, or global RNG state.",
			seedA, requests, seedCDifferent, firstDivergence,
		)
	}
	fmt.Printf("\n%s\n", finding)

	res := Experiment005FResult{
		Experiment: "005-F-seeded-randomness", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Requests: requests, SeedA: seedA, SeedBSameAsA: seedBSameAsA, SeedCDifferent: seedCDifferent,
		RunAEqualsRunB: sameSeedIdentical, RunAEqualsRunC: diffSeedIdentical, FirstDivergenceIndex: firstDivergence,
		DistributionA: distA, DistributionC: distC, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005F-seeded-randomness.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	if !sameSeedIdentical {
		log.Fatal("experiment 005-F failed: same-seed determinism was violated")
	}

	fmt.Println("\nExperiment 005-F complete.")
}
