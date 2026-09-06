// Command experiment-013h is Stage 13 Program H: does "queueing pressure
// partially counteracts the cache-affinity bonus" (Stage 12's own B1
// finding: 0% return at Capacity=0, 68% at Capacity=1) generalize across
// capacity AND cache-weight combinations, or was it specific to one
// scenario? Reuses Stage 11/12's own B1 scenario (edge-a crashes at
// t=1s, recovers at t=2s, a hot key's affinity target degrades and
// recovers) across a small capacity x cache-weight grid.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/013-regime-discovery/results"
const horizon = 4 * time.Second

func targets(capacity int) []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
}

type Cell struct {
	Capacity        int     `json:"capacity"`
	CacheWeight     float64 `json:"cache_weight"`
	MeanMs          float64 `json:"mean_ms"`
	PostRecoveryPct int     `json:"post_recovery_return_pct"`
}

func hottestKey(records []replay.SelectionRecord) string {
	counts := map[string]int{}
	for _, r := range records {
		counts[r.Key]++
	}
	best, bestN := "", 0
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=============================================================================")
	fmt.Println(" Experiment 013-H: Cache-Affinity Self-Healing -- Generalization Across Capacity/Weight")
	fmt.Println("=============================================================================")

	sched, err := chaos.ParseYAML(strings.NewReader(
		"- at: 1s\n  target: edge-a\n  action: crash\n- at: 2s\n  target: edge-a\n  action: recover\n"))
	if err != nil {
		log.Fatalf("parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("compiling chaos: %v", err)
	}

	var cells []Cell
	capacities := []int{0, 1, 2, 3}
	cacheWeights := []float64{0.02, 0.1, 0.3}
	seedCounter := 0
	for _, capacity := range capacities {
		fmt.Printf("\n-- Capacity=%d --\n", capacity)
		for _, cacheWeight := range cacheWeights {
			cfg := proxy.DefaultAdaptiveConfig()
			cfg.Weights.Cache = cacheWeight
			seedCounter++
			rootSeed := int64(13800 + seedCounter)
			seeds := replay.DeriveSeeds(rootSeed)
			arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
				Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
			}, seeds.Traffic)
			if err != nil {
				log.Fatalf("cap=%d/cache=%.2f: generating traffic: %v", capacity, cacheWeight, err)
			}
			scenario := replay.Scenario{
				Targets: targets(capacity), Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
				Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
			}
			v := engine.NewVirtualEngine()
			exp := engine.Experiment{ID: fmt.Sprintf("013h-cap%d-cache%.2f", capacity, cacheWeight), Scenario: scenario, Policy: replay.AdaptivePolicyWithConfig(cfg)}
			result, err := v.Run(exp)
			if err != nil {
				log.Fatalf("cap=%d/cache=%.2f: %v", capacity, cacheWeight, err)
			}
			wr := result.WorldResult
			ms := make([]float64, len(wr.Completions))
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			mean, _ := statistics.Mean(ms)

			hotKey := hottestKey(wr.Records)
			postRecovery, toA := 0, 0
			for _, r := range wr.Records {
				if r.Key != hotKey || r.VirtualTimeMs < 2000 {
					continue
				}
				postRecovery++
				if r.Target == "edge-a" {
					toA++
				}
			}
			pct := 0
			if postRecovery > 0 {
				pct = int(100 * float64(toA) / float64(postRecovery))
			}
			cell := Cell{Capacity: capacity, CacheWeight: cacheWeight, MeanMs: mean, PostRecoveryPct: pct}
			cells = append(cells, cell)
			fmt.Printf("  cache_weight=%.2f  mean=%9.2fms  post_recovery_return=%d%%\n", cacheWeight, mean, pct)
		}
	}

	fmt.Println("\n--- Does the self-healing pattern hold across the grid? ---")
	fmt.Println("capacity  cache=0.02  cache=0.10  cache=0.30")
	byCell := map[string]Cell{}
	for _, c := range cells {
		byCell[fmt.Sprintf("%d/%.2f", c.Capacity, c.CacheWeight)] = c
	}
	for _, capacity := range capacities {
		fmt.Printf("%-8d  ", capacity)
		for _, cw := range cacheWeights {
			fmt.Printf("%8d%%  ", byCell[fmt.Sprintf("%d/%.2f", capacity, cw)].PostRecoveryPct)
		}
		fmt.Println()
	}

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
	}{Experiment: "013-H-cache-affinity-generalization", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013H-cache-affinity-generalization.json"), b, 0644)
	fmt.Println("\nExperiment 013-H complete.")
}
