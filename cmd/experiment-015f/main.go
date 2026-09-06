// Command experiment-015f is Stage 15 Section 26: real-engine
// validation of the concentration-proneness mechanism -- extending
// Stage 14's own experiment-014e (which validated that Adaptive beats
// EWMA under an honest, validated real concurrency ceiling) to also test
// Stage 15's own falsifier (round-robin beating EWMA), the new claim
// this stage's own mechanism work depends on.
//
// Disclosed limitation, stated before running (per Section 26's own
// instruction not to claim exact numeric agreement, only directional
// agreement): internal/engine.RealMetrics exposes only a latency
// histogram and per-target completion counts (confirmed by reading
// engine.go directly) -- no per-request dispatch/completion timeline
// the way replay.WorldResult provides. Committed backlog, as measured
// on the virtual engine by internal/backlog, is therefore NOT directly
// measurable on the real engine with existing instrumentation. This
// experiment validates the DIRECTIONAL claim only (does the same
// concentration -> collapse relationship, and the round-robin-beats-
// EWMA falsifier, appear in both engines), not committed backlog itself
// -- adding new RealEngine instrumentation was considered and rejected,
// since Stage 15 is explicitly not a platform-feature stage and the
// directional question can be answered without it.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/015-mechanism-identification/results"

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
}

func edges() map[string]time.Duration {
	return map[string]time.Duration{
		"edge-a": 15 * time.Millisecond,
		"edge-b": 30 * time.Millisecond,
		"edge-c": 60 * time.Millisecond,
	}
}

type Cell struct {
	Level    string  `json:"level"`
	Requests int     `json:"requests"`
	Policy   string  `json:"policy"`
	P50Ms    float64 `json:"p50_ms"`
	P99Ms    float64 `json:"p99_ms"`
	MaxShare float64 `json:"max_share"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-F: Real-Engine Validation of the Concentration-Proneness Mechanism")
	fmt.Println(" (Section 26 -- extends 014e's validated ceiling=1 mechanism to Stage 15's own falsifier)")
	fmt.Println("=====================================================================================")

	horizon := 4 * time.Second
	// Same validated ceiling (MaxConnsPerHost=1, matching the virtual
	// model's own Capacity=1 exactly) as experiment-014e -- already
	// policy-neutrally validated there (raw atomic-counter probe
	// confirming the ceiling genuinely bounds concurrency); not
	// re-validated here to avoid duplicating that check.
	levels := []struct {
		name     string
		requests int
	}{
		{"below_ceiling", 30},
		{"near_ceiling", 75},
		{"above_ceiling", 400},
	}

	var cells []Cell
	for i, level := range levels {
		fmt.Printf("\n-- %s (Requests=%d, MaxConnsPerHost=1) --\n", level.name, level.requests)
		seeds := replay.DeriveSeeds(int64(15700 + i))
		for _, ps := range []struct {
			name string
			spec replay.PolicySpec
		}{{"round-robin", replay.RoundRobinPolicy()}, {"ewma", replay.EWMAPolicy()}, {"adaptive", replay.AdaptivePolicy()}} {
			exp := engine.Experiment{
				ID:       fmt.Sprintf("015f-%s-%s", level.name, ps.name),
				Scenario: replay.Scenario{Targets: targets(), Seeds: seeds},
				Policy:   ps.spec,
				Real: &engine.RealExperimentConfig{
					Edges:           edges(),
					TrafficPattern:  traffic.Constant,
					TrafficParams:   traffic.Params{Requests: level.requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)},
					MaxConnsPerHost: 1,
				},
			}
			r := engine.NewRealEngine()
			result, err := r.Run(exp)
			if err != nil {
				log.Fatalf("%s/%s: %v", level.name, ps.name, err)
			}
			p50, p99 := 0.0, 0.0
			if result.Real.Metrics.Histogram != nil {
				p50 = float64(result.Real.Metrics.Histogram.ValueAtPercentile(50)) / 1e6
				p99 = float64(result.Real.Metrics.Histogram.ValueAtPercentile(99)) / 1e6
			}
			total, maxCount := 0, 0
			for _, n := range result.Real.Metrics.RequestsTotal {
				total += int(n)
				if int(n) > maxCount {
					maxCount = int(n)
				}
			}
			maxShare := 0.0
			if total > 0 {
				maxShare = float64(maxCount) / float64(total)
			}
			cell := Cell{Level: level.name, Requests: level.requests, Policy: ps.name, P50Ms: p50, P99Ms: p99, MaxShare: maxShare}
			cells = append(cells, cell)
			fmt.Printf("  %-12s p50=%7.2fms  p99=%9.2fms  max_share=%.3f  completed=%d/%d\n", ps.name, p50, p99, maxShare, result.Real.Requests, level.requests)
		}
	}

	fmt.Println("\n--- Does round-robin beat EWMA on the REAL engine too (Stage 15's own falsifier, extending 014e)? ---")
	byLevel := map[string]map[string]Cell{}
	for _, c := range cells {
		if byLevel[c.Level] == nil {
			byLevel[c.Level] = map[string]Cell{}
		}
		byLevel[c.Level][c.Policy] = c
	}
	rrBeatsEwmaCount := 0
	for _, level := range []string{"below_ceiling", "near_ceiling", "above_ceiling"} {
		rr, e, a := byLevel[level]["round-robin"], byLevel[level]["ewma"], byLevel[level]["adaptive"]
		rrWins := rr.P99Ms < e.P99Ms
		if rrWins {
			rrBeatsEwmaCount++
		}
		fmt.Printf("%-14s rr_p99=%9.2fms  ewma_p99=%9.2fms  adaptive_p99=%9.2fms  rr_beats_ewma=%v\n", level, rr.P99Ms, e.P99Ms, a.P99Ms, rrWins)
	}
	fmt.Printf("\nround-robin beat EWMA on p99 in %d/3 levels on the REAL engine.\n", rrBeatsEwmaCount)
	if rrBeatsEwmaCount >= 2 {
		fmt.Println("The falsifier's DIRECTION reproduces on the real engine, extending 014e's own Adaptive-vs-EWMA")
		fmt.Println("reproduction to Stage 15's concentration-proneness mechanism more broadly -- not just one policy pair.")
	} else {
		fmt.Println("The falsifier's direction does NOT clearly reproduce on the real engine -- document as a genuine")
		fmt.Println("point of virtual-vs-real divergence, per Section 26's own instruction, not smoothed over.")
	}

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
		Limitation string `json:"limitation"`
	}{
		Experiment: "015-F-real-engine-validation", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells,
		Limitation: "committed backlog is not directly measurable on the real engine with existing instrumentation (RealMetrics exposes only a histogram and per-target completion counts, no per-request dispatch/completion timeline); this experiment validates directional agreement only (p99 ranking, max_share concentration), not committed backlog itself",
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015F-real-engine-validation.json"), b, 0644)
	fmt.Println("\nExperiment 015-F complete.")
}
