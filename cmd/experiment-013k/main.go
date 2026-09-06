// Command experiment-013k is Stage 13 Section 25: virtual-vs-real
// triangulation, NOT a full-matrix reproduction. RealEngine does not
// consume the virtual Capacity/ServiceTimeSchedule abstraction at all --
// there is no way to construct a literally rho-equivalent real scenario.
// Instead this selects three REAL concurrent-request-volume levels
// (low/medium/high) on the same 15/30/60ms edge topology and asks only
// the qualitative question Stage 12's own §25 instruction allows: does
// EWMA's relative advantage over Adaptive shrink and reverse as genuine
// concurrent pressure rises, the same DIRECTION the virtual capacity
// sweep showed -- not whether the numbers match.
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

const outDirName = "experiments/013-regime-discovery/results"

type Cell struct {
	Level        string  `json:"level"`
	RequestCount int     `json:"request_count"`
	Policy       string  `json:"policy"`
	P50Ms        float64 `json:"p50_ms"`
	MaxShare     float64 `json:"max_share"`
}

func edges() map[string]time.Duration {
	return map[string]time.Duration{
		"edge-a": 15 * time.Millisecond,
		"edge-b": 30 * time.Millisecond,
		"edge-c": 60 * time.Millisecond,
	}
}

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================")
	fmt.Println(" Experiment 013-K: Virtual-vs-Real Triangulation (qualitative only)")
	fmt.Println(" NOTE: RealEngine does not consume Capacity/ServiceTimeSchedule -- these")
	fmt.Println(" are NOT rho-equivalent to the virtual sweep. Direction only, not numbers.")
	fmt.Println("=====================================================================")

	horizon := 4 * time.Second
	var cells []Cell
	for i, level := range []struct {
		name     string
		requests int
	}{
		{"low", 40},
		{"medium", 150},
		{"high", 400},
	} {
		fmt.Printf("\n-- %s concurrency (Requests=%d over %v) --\n", level.name, level.requests, horizon)
		seeds := replay.DeriveSeeds(int64(14000 + i))
		for _, ps := range []struct {
			name string
			spec replay.PolicySpec
		}{{"ewma", replay.EWMAPolicy()}, {"adaptive", replay.AdaptivePolicy()}} {
			exp := engine.Experiment{
				ID: fmt.Sprintf("013k-%s-%s", level.name, ps.name),
				Scenario: replay.Scenario{
					Targets: targets(), Seeds: seeds,
				},
				Policy: ps.spec,
				Real: &engine.RealExperimentConfig{
					Edges:          edges(),
					TrafficPattern: traffic.Constant,
					TrafficParams:  traffic.Params{Requests: level.requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)},
				},
			}
			r := engine.NewRealEngine()
			result, err := r.Run(exp)
			if err != nil {
				log.Fatalf("%s/%s: %v", level.name, ps.name, err)
			}
			p50 := 0.0
			if result.Real.Metrics.Histogram != nil {
				p50 = float64(result.Real.Metrics.Histogram.ValueAtPercentile(50)) / 1e6
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
			cell := Cell{Level: level.name, RequestCount: level.requests, Policy: ps.name, P50Ms: p50, MaxShare: maxShare}
			cells = append(cells, cell)
			fmt.Printf("  %-10s p50=%7.2fms  max_share=%.3f\n", ps.name, p50, maxShare)
		}
	}

	fmt.Println("\n--- Qualitative comparison to the virtual capacity sweep ---")
	byLevel := map[string]map[string]Cell{}
	for _, c := range cells {
		if byLevel[c.Level] == nil {
			byLevel[c.Level] = map[string]Cell{}
		}
		byLevel[c.Level][c.Policy] = c
	}
	for _, level := range []string{"low", "medium", "high"} {
		e, a := byLevel[level]["ewma"], byLevel[level]["adaptive"]
		winner := "EWMA"
		if a.P50Ms < e.P50Ms {
			winner = "Adaptive"
		}
		fmt.Printf("%-8s concurrency: ewma_p50=%7.2fms  adaptive_p50=%7.2fms  -- %s (real engine, real OS-level concurrency, no explicit slot model)\n",
			level, e.P50Ms, a.P50Ms, winner)
	}
	fmt.Println("\nThe virtual sweep's finding was a SHARP, capacity-threshold-dependent reversal (Section 7.1,")
	fmt.Println("013a/013b/013c). The real engine has no equivalent literal threshold to cross -- any agreement")
	fmt.Println("or disagreement in DIRECTION here is triangulating evidence, not a replication of the virtual result.")

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
	}{Experiment: "013-K-virtual-real-triangulation", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013K-virtual-real-triangulation.json"), b, 0644)
	fmt.Println("\nExperiment 013-K complete.")
}
