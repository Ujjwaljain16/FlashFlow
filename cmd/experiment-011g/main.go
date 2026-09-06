// Command experiment-011g is Stage 11's Program G: a seed and
// reproducibility attack. Two parts: (1) run the identical experiment
// repeatedly and confirm byte-identical results; (2) vary each of the
// four SeedTree axes (Traffic/Topology/Failure/Policy) one at a time,
// holding the other three fixed, and confirm ONLY the intended part of
// the scenario/outcome changes -- particularly important since Stage 10
// widened the scenario into independent seed axes specifically to make
// this kind of isolation possible (internal/tuning/scenario.go's own
// Generate is the actual generator that consumes all four axes; the
// literal-topology scenarios built by cmd/experiment-011a/b/c/d don't
// exercise Topology/Failure-seed-driven generation at all, so this
// program uses tuning.DefaultScenarioSpace().Generate for a genuine
// test of axis independence).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/tuning"
)

const outDirName = "experiments/011-research-validation/results"

type AxisCheck struct {
	Axis             string `json:"axis"`
	TargetsChanged   bool   `json:"targets_changed"`
	ArrivalsChanged  bool   `json:"arrivals_changed"`
	FailuresChanged  bool   `json:"failures_changed"`
	ExpectedToChange string `json:"expected_to_change"`
	IsolationHolds   bool   `json:"isolation_holds"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=================================================================")
	fmt.Println(" Experiment 011-G: Seed and Reproducibility Attack")
	fmt.Println("=================================================================")

	reproOK := checkRepeatRunReproducibility()
	axisResults := checkAxisIndependence()
	policyAxisOK, policyAxisNote := checkPolicySeedIsolation()

	out := struct {
		Experiment               string      `json:"experiment"`
		Timestamp                string      `json:"timestamp"`
		RepeatRunReproducible    bool        `json:"repeat_run_reproducible"`
		AxisIndependence         []AxisCheck `json:"axis_independence"`
		PolicySeedIsolationHolds bool        `json:"policy_seed_isolation_holds"`
		PolicySeedNote           string      `json:"policy_seed_note"`
	}{
		Experiment: "011-G-reproducibility-attack", Timestamp: time.Now().UTC().Format(time.RFC3339),
		RepeatRunReproducible: reproOK, AxisIndependence: axisResults,
		PolicySeedIsolationHolds: policyAxisOK, PolicySeedNote: policyAxisNote,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011G-reproducibility-attack.json"), b, 0644)
	fmt.Println("\nExperiment 011-G complete.")
}

// checkRepeatRunReproducibility runs the identical Experiment 3 times and
// compares full WorldResult traces, not just summary stats -- a summary
// stat could match by coincidence even if underlying decisions differed.
func checkRepeatRunReproducibility() bool {
	fmt.Println("\n--- G1: Repeat-run reproducibility (3x identical config) ---")
	seeds := replay.DeriveSeeds(5001)
	scenario := tuning.DefaultScenarioSpace().Generate(seeds)
	exp := engine.Experiment{ID: "011g-repro", Scenario: scenario, Policy: replay.AdaptivePolicy()}
	v := engine.NewVirtualEngine()

	var results []*replay.WorldResult
	for i := 0; i < 3; i++ {
		r, err := v.Run(exp)
		if err != nil {
			log.Fatalf("repro run %d: %v", i, err)
		}
		results = append(results, r.WorldResult)
	}
	allIdentical := true
	for i := 1; i < len(results); i++ {
		if !reflect.DeepEqual(results[0].Records, results[i].Records) ||
			!reflect.DeepEqual(results[0].Completions, results[i].Completions) ||
			results[0].RejectedCount != results[i].RejectedCount {
			allIdentical = false
		}
	}
	fmt.Printf("  3 runs, identical Records+Completions+RejectedCount: %v\n", allIdentical)
	return allIdentical
}

// checkAxisIndependence holds a baseline SeedTree fixed except for one
// axis at a time, regenerates the Scenario via tuning's own generator,
// and checks that ONLY the part of the scenario tied to that axis
// changed.
func checkAxisIndependence() []AxisCheck {
	fmt.Println("\n--- G2: SeedTree axis independence ---")
	space := tuning.DefaultScenarioSpace()
	base := replay.SeedTree{Global: 0, Traffic: 100, Topology: 200, Failure: 300, Policy: 400}
	baseline := space.Generate(base)

	variants := []struct {
		axis    string
		mutate  func(replay.SeedTree) replay.SeedTree
		expects string
	}{
		{"Traffic", func(s replay.SeedTree) replay.SeedTree { s.Traffic = 999; return s }, "arrivals only"},
		{"Topology", func(s replay.SeedTree) replay.SeedTree { s.Topology = 999; return s }, "targets only"},
		{"Failure", func(s replay.SeedTree) replay.SeedTree { s.Failure = 999; return s }, "failures only"},
	}

	var results []AxisCheck
	for _, v := range variants {
		mutated := space.Generate(v.mutate(base))
		targetsChanged := !reflect.DeepEqual(baseline.Targets, mutated.Targets)
		arrivalsChanged := !reflect.DeepEqual(baseline.Arrivals, mutated.Arrivals)
		failuresChanged := !reflect.DeepEqual(baseline.Failures, mutated.Failures)

		holds := false
		switch v.axis {
		case "Traffic":
			holds = arrivalsChanged && !targetsChanged && !failuresChanged
		case "Topology":
			holds = targetsChanged && !arrivalsChanged && !failuresChanged
		case "Failure":
			holds = failuresChanged && !targetsChanged && !arrivalsChanged
		}
		results = append(results, AxisCheck{
			Axis: v.axis, TargetsChanged: targetsChanged, ArrivalsChanged: arrivalsChanged,
			FailuresChanged: failuresChanged, ExpectedToChange: v.expects, IsolationHolds: holds,
		})
		fmt.Printf("  vary %-9s only -> targets_changed=%-5v arrivals_changed=%-5v failures_changed=%-5v  isolation_holds=%v\n",
			v.axis, targetsChanged, arrivalsChanged, failuresChanged, holds)
	}
	return results
}

// checkPolicySeedIsolation is a deliberately nuanced check: seeds.Policy
// is NOT consumed at scenario-generation time at all (tuning.Generate
// never reads it) -- it's consumed at ROUTING time, and only by policies
// that need their own randomness. Of the 6 policies, only p2c-load reads
// seeds.Policy (internal/replay/policies.go's P2CLoadPolicy). This means
// "does varying seeds.Policy change the outcome" has a policy-dependent
// answer, not a single yes/no -- reported explicitly rather than
// collapsed into one number.
func checkPolicySeedIsolation() (bool, string) {
	fmt.Println("\n--- G3: Policy-seed isolation (policy-dependent, checked explicitly) ---")
	space := tuning.DefaultScenarioSpace()
	base := replay.SeedTree{Global: 0, Traffic: 100, Topology: 200, Failure: 300, Policy: 400}
	scenario := space.Generate(base) // SAME scenario for both policy-seed variants -- only Policy differs
	altSeeds := base
	altSeeds.Policy = 999
	scenarioAlt := scenario
	scenarioAlt.Seeds = altSeeds

	v := engine.NewVirtualEngine()
	allCorrect := true
	var notes []string
	for _, ps := range []struct {
		name           string
		spec           replay.PolicySpec
		consumesPolicy bool
	}{
		{"round-robin", replay.RoundRobinPolicy(), false},
		{"ewma", replay.EWMAPolicy(), false},
		{"adaptive", replay.AdaptivePolicy(), false},
		{"p2c-load", replay.P2CLoadPolicy(), true},
	} {
		exp1 := engine.Experiment{ID: "011g-policy-seed-a", Scenario: scenario, Policy: ps.spec}
		exp2 := engine.Experiment{ID: "011g-policy-seed-b", Scenario: scenarioAlt, Policy: ps.spec}
		r1, err := v.Run(exp1)
		if err != nil {
			log.Fatalf("policy-seed check %s (seed A): %v", ps.name, err)
		}
		r2, err := v.Run(exp2)
		if err != nil {
			log.Fatalf("policy-seed check %s (seed B): %v", ps.name, err)
		}
		changed := !reflect.DeepEqual(r1.WorldResult.Records, r2.WorldResult.Records)
		correct := changed == ps.consumesPolicy
		if !correct {
			allCorrect = false
		}
		note := fmt.Sprintf("%s: consumes seeds.Policy=%v, outcome changed=%v, matches expectation=%v", ps.name, ps.consumesPolicy, changed, correct)
		notes = append(notes, note)
		fmt.Printf("  %s\n", note)
	}
	return allCorrect, fmt.Sprintf("%v", notes)
}
