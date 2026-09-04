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
	"flashflow/internal/health"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

// StateTransition is one recorded change in a target's observed health
// state -- deliberately not every probe result, just the moments the
// state machine actually changed, since that's what "reproduced
// precisely" means for this experiment.
type StateTransition struct {
	VirtualTimeMs float64      `json:"virtual_time_ms"`
	Target        string       `json:"target"`
	NewState      health.State `json:"new_state"`
}

type Experiment005DResult struct {
	Experiment       string            `json:"experiment"`
	Timestamp        string            `json:"timestamp"`
	Target           string            `json:"target"`
	ProbeIntervalMs  int64             `json:"probe_interval_ms"`
	FailureAtMs      int64             `json:"failure_at_ms"`
	RecoveryAtMs     int64             `json:"recovery_at_ms"`
	Transitions      []StateTransition `json:"transitions"`
	Runs             int               `json:"runs"`
	AllRunsIdentical bool              `json:"all_runs_identical"`
	Findings         string            `json:"findings"`
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// buildAndRun constructs a fresh engine and health.Registry -- reused
// completely unmodified -- and drives it from a ground-truth up/down
// schedule for target, checked by a virtual probe Ticker every interval.
// The ground-truth map is deliberately kept separate from the registry:
// the registry only ever learns about the world through probe results,
// exactly mirroring the real system's own asymmetry between "the target
// is actually down" and "the health checker has detected it."
func buildAndRun(target string, interval time.Duration, failAt, recoverAt, runUntil clock.VirtualTime) []StateTransition {
	e := vtime.NewEngine(0)
	registry := health.NewRegistry(e.Clock(), health.DefaultConfig())
	registry.RegisterTarget(target)

	up := true
	lastState := health.StateHealthy
	var transitions []StateTransition

	e.Schedule(failAt, func() { up = false })
	e.Schedule(recoverAt, func() { up = true })

	if _, err := e.NewTicker(0, interval, func() {
		newState := registry.RecordProbeResult(target, up)
		if newState != lastState {
			transitions = append(transitions, StateTransition{
				VirtualTimeMs: msF(time.Duration(e.Now())), Target: target, NewState: newState,
			})
			e.Record("health_state_changed", target, map[string]any{"state": string(newState)})
			lastState = newState
		}
	}); err != nil {
		log.Fatalf("failed to start probe ticker: %v", err)
	}

	if err := e.RunUntil(runUntil); err != nil {
		log.Fatalf("scenario failed: %v", err)
	}
	return transitions
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-D: Deterministic Failure Schedule")
	fmt.Println(" Can failures and recovery be reproduced precisely, at exact virtual timestamps?")
	fmt.Println("==========================================================================================")

	const target = "edge-2"
	const interval = 1 * time.Second
	failAt := clock.VirtualTime(5 * time.Second)
	recoverAt := clock.VirtualTime(10 * time.Second)
	runUntil := clock.VirtualTime(15 * time.Second)

	baseline := buildAndRun(target, interval, failAt, recoverAt, runUntil)

	fmt.Println("\nObserved state transitions (run 0):")
	for _, tr := range baseline {
		fmt.Printf("  t=%-7.0fms %-8s -> %s\n", tr.VirtualTimeMs, tr.Target, tr.NewState)
	}

	const runs = 20
	allIdentical := true
	for i := 1; i < runs; i++ {
		trace := buildAndRun(target, interval, failAt, recoverAt, runUntil)
		if !reflect.DeepEqual(trace, baseline) {
			allIdentical = false
			log.Printf("run %d diverged from run 0", i)
			break
		}
	}

	var finding string
	if allIdentical {
		finding = fmt.Sprintf(
			"Failure at t=%.0fs and recovery at t=%.0fs (probe interval %v) reproduced the identical %d-transition "+
				"state sequence across all %d runs: %v. health.Registry required no modification -- only its "+
				"probe-scheduling mechanism (a real ticker + real HTTP in health.Checker) was replaced with a "+
				"virtual Ticker feeding it a ground-truth schedule instead of a live network probe.",
			failAt.Sub(0).Seconds(), recoverAt.Sub(0).Seconds(), interval, len(baseline), runs, transitionSummary(baseline),
		)
	} else {
		finding = "DETERMINISM VIOLATED: see log output above for the diverging run."
	}
	fmt.Printf("\n%s\n", finding)

	res := Experiment005DResult{
		Experiment: "005-D-deterministic-failure-schedule", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Target: target, ProbeIntervalMs: interval.Milliseconds(),
		FailureAtMs: failAt.Nanoseconds() / int64(time.Millisecond), RecoveryAtMs: recoverAt.Nanoseconds() / int64(time.Millisecond),
		Transitions: baseline, Runs: runs, AllRunsIdentical: allIdentical, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005D-failure-schedule.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	if !allIdentical {
		log.Fatal("experiment 005-D failed: determinism was violated")
	}

	fmt.Println("\nExperiment 005-D complete.")
}

func transitionSummary(transitions []StateTransition) string {
	s := ""
	for i, tr := range transitions {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s@%.0fms", tr.NewState, tr.VirtualTimeMs)
	}
	return s
}
