package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

const outDirName = "experiments/007-adaptive-replay/results"

// keyFor mirrors 007-B/007-E's rotating-key pattern.
func keyFor(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	return fmt.Sprintf("/cold-%d", i%3)
}

// heterogeneousScenario is 007-B's exact heterogeneous cell (1 slow=100ms,
// 2 fast=20ms, 300 requests at 5ms spacing), expressed as a replay.Scenario
// -- the real, previously-studied scenario this experiment validates the
// replay engine against, as opposed to internal/replay/world_test.go's
// synthetic unit-test scenario.
func heterogeneousScenario(seed int64) replay.Scenario {
	const requests = 300
	const spacing = 5 * time.Millisecond
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: keyFor(i)}
	}
	return replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "edge-a-slow", ServiceTime: 100 * time.Millisecond},
			{Name: "edge-b-fast", ServiceTime: 20 * time.Millisecond},
			{Name: "edge-c-fast", ServiceTime: 20 * time.Millisecond},
		},
		Arrivals: arrivals,
		Seeds:    replay.DeriveSeeds(seed),
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 007-F: Counterfactual Identity")
	fmt.Println(" Recorded, auditable confirmation of internal/replay's three load-bearing properties --")
	fmt.Println(" identity, divergence-only-after-intervention, isolation -- on a real, previously-studied")
	fmt.Println(" scenario (007-B's heterogeneous cell), independent of internal/replay/world_test.go's")
	fmt.Println(" unit tests.")
	fmt.Println("==========================================================================================")

	spec := replay.AdaptivePolicy()

	// --- Identity ---
	base := heterogeneousScenario(1)
	result1, err := replay.RunWorld(base, spec)
	if err != nil {
		log.Fatalf("identity check: 1st run failed: %v", err)
	}
	result2, err := replay.RunWorld(base, spec)
	if err != nil {
		log.Fatalf("identity check: 2nd run failed: %v", err)
	}
	_, identityDiverged := replay.FirstDivergence(result1.Trace, result2.Trace)
	recordsIdentical := reflect.DeepEqual(result1.Records, result2.Records)
	identityConfirmed := !identityDiverged && recordsIdentical
	fmt.Printf("\nIdentity: two runs of the identical Scenario+PolicySpec produced %d-event traces, identical: %v\n",
		len(result1.Trace), identityConfirmed)

	// --- Divergence only after intervention ---
	const cutoff = clock.VirtualTime(150 * time.Millisecond)
	withHealth := base
	withHealth.UseHealthRegistry = true // see world_test.go's comment: both sides need identical probe machinery
	withHealth.Horizon = clock.VirtualTime(2 * time.Second)
	withoutFailure := withHealth
	withFailure := withHealth
	withFailure.Failures = []replay.FailureWindow{
		{Target: "edge-b-fast", DownAt: cutoff, UpAt: cutoff.Add(500 * time.Millisecond)},
	}

	resultNoFail, err := replay.RunWorld(withoutFailure, spec)
	if err != nil {
		log.Fatalf("divergence check: no-failure run failed: %v", err)
	}
	resultFail, err := replay.RunWorld(withFailure, spec)
	if err != nil {
		log.Fatalf("divergence check: with-failure run failed: %v", err)
	}
	divIdx, diverged := replay.FirstDivergence(resultNoFail.Trace, resultFail.Trace)
	divergenceConfirmed := diverged
	var divergenceTimeMs float64
	var divergenceNotBeforeCutoff bool
	if diverged {
		divergenceTimeMs = float64(resultNoFail.Trace[divIdx].Time) / 1e6
		divergenceNotBeforeCutoff = resultNoFail.Trace[divIdx].Time >= cutoff
	}
	fmt.Printf("Divergence: introducing a failure at t=%v caused the trace to diverge at t=%.0fms (>= cutoff: %v)\n",
		time.Duration(cutoff), divergenceTimeMs, divergenceNotBeforeCutoff)

	// --- Isolation ---
	beforeIsolation, err := replay.RunWorld(base, spec)
	if err != nil {
		log.Fatalf("isolation check: before run failed: %v", err)
	}
	unrelated := heterogeneousScenario(999)
	unrelated.UseHealthRegistry = true
	unrelated.Horizon = clock.VirtualTime(2 * time.Second)
	unrelated.Failures = []replay.FailureWindow{{Target: "edge-a-slow", DownAt: 0, UpAt: clock.VirtualTime(1 * time.Second)}}
	if _, err := replay.RunWorld(unrelated, replay.P2CLoadPolicy()); err != nil {
		log.Fatalf("isolation check: unrelated interleaved run failed: %v", err)
	}
	afterIsolation, err := replay.RunWorld(base, spec)
	if err != nil {
		log.Fatalf("isolation check: after run failed: %v", err)
	}
	_, isolationDiverged := replay.FirstDivergence(beforeIsolation.Trace, afterIsolation.Trace)
	isolationConfirmed := !isolationDiverged
	fmt.Printf("Isolation: running an unrelated Scenario+PolicySpec in between left this Scenario's outcome unchanged: %v\n",
		isolationConfirmed)

	allConfirmed := identityConfirmed && divergenceConfirmed && divergenceNotBeforeCutoff && isolationConfirmed

	finding := fmt.Sprintf(
		"On 007-B's real heterogeneous scenario (not a synthetic unit-test fixture), all three properties "+
			"internal/replay's design depends on held: identity (two runs of the same Scenario+PolicySpec produced "+
			"byte-for-byte identical %d-event traces), divergence-only-after-intervention (introducing a failure at "+
			"t=%v caused the trace to diverge starting at t=%.0fms, at or after the intervention and never before "+
			"it -- a causality check, not just an equality check), and isolation (an unrelated Scenario+PolicySpec run "+
			"in between two identical runs changed nothing). This is the auditable, recorded counterpart to "+
			"internal/replay/world_test.go's unit tests, matching the precedent set by 006-A (statistics) and 007-A "+
			"(adaptive signals): unit tests establish internal correctness, this experiment establishes the same "+
			"claims as evidence, on a scenario this project has already studied and trusted.",
		len(result1.Trace), time.Duration(cutoff), divergenceTimeMs,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment                string  `json:"experiment"`
		Timestamp                 string  `json:"timestamp"`
		IdentityConfirmed         bool    `json:"identity_confirmed"`
		TraceLength               int     `json:"trace_length"`
		DivergenceConfirmed       bool    `json:"divergence_confirmed"`
		DivergenceTimeMs          float64 `json:"divergence_time_ms"`
		DivergenceNotBeforeCutoff bool    `json:"divergence_not_before_cutoff"`
		CutoffMs                  float64 `json:"cutoff_ms"`
		IsolationConfirmed        bool    `json:"isolation_confirmed"`
		AllConfirmed              bool    `json:"all_confirmed"`
		Findings                  string  `json:"findings"`
	}{
		Experiment: "007-F-counterfactual-identity", Timestamp: time.Now().UTC().Format(time.RFC3339),
		IdentityConfirmed: identityConfirmed, TraceLength: len(result1.Trace),
		DivergenceConfirmed: divergenceConfirmed, DivergenceTimeMs: divergenceTimeMs,
		DivergenceNotBeforeCutoff: divergenceNotBeforeCutoff, CutoffMs: float64(cutoff) / 1e6,
		IsolationConfirmed: isolationConfirmed, AllConfirmed: allConfirmed, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "007F-counterfactual-identity.json"), b, 0644)

	if !allConfirmed {
		log.Fatal("experiment 007-F failed: one or more counterfactual properties did not hold")
	}

	fmt.Println("\nExperiment 007-F complete.")
}
