// Command experiment-013b is Stage 13 Section 21: the "Capacity=1
// artifact" hypothesis test, combined with the core Program D rho
// analysis. Stage 12's strongest reversal happened at Capacity=1 --
// this experiment asks whether "one slot" itself is special, or whether
// what actually matters is the NORMALIZED offered-load/capacity ratio
// (rho). Constructs Capacity=1/2/4/8 scenarios on the severe-
// heterogeneity topology with arrival rate scaled PROPORTIONALLY to
// capacity (via Requests, since traffic.Generate's Constant pattern
// ignores BaseRate entirely -- the real rate is Requests/Horizon,
// confirmed by direct inspection of internal/traffic/generator.go
// before assuming otherwise), so every cell targets approximately the
// SAME rho at the concentrated target.
//
// Uses an OFFERED rho (computed from dispatch records, not completions)
// rather than attribution.UtilizationFromWorld directly -- experiment-
// 013a found that completion-based rho understates true load whenever
// horizon truncation leaves work queued past the run's end, which
// happens routinely at these higher capacities/rates.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/engine"
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
	Capacity          int     `json:"capacity"`
	Requests          int     `json:"requests"`
	EffectiveRatePerS float64 `json:"effective_rate_per_sec"`
	Policy            string  `json:"policy"`
	MeanMs            float64 `json:"mean_ms"`
	Completed         int     `json:"completed"`
	MaxShare          float64 `json:"max_share"`
	OfferedRhoMax     float64 `json:"offered_rho_max"` // dispatch-based, immune to horizon-truncation bias
}

func policySpecs() []struct {
	name string
	spec replay.PolicySpec
} {
	return []struct {
		name string
		spec replay.PolicySpec
	}{
		{"ewma", replay.EWMAPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

// offeredRho computes, for each target, (dispatch count / horizon) *
// ServiceTime / Capacity -- the OFFERED utilization for a multi-slot
// (c-server) target, using SelectionRecords (every accepted routing
// decision) rather than Completions (which under-count whenever the
// horizon truncates a request still sitting in queue). Capacity <= 0
// (infinite) is treated as 1 here purely to keep this specific ratio
// finite and comparable across cells -- it does NOT mean the target
// behaves like a 1-slot server; it means "no capacity-normalization
// applies," so this rho reduces to the plain lambda*serviceTime value
// used everywhere else in this project for the flat model.
func offeredRho(wr *replay.WorldResult, targetsList []replay.TargetProfile, horizon time.Duration) map[string]float64 {
	svc := make(map[string]time.Duration, len(targetsList))
	capacity := make(map[string]int, len(targetsList))
	for _, t := range targetsList {
		svc[t.Name] = t.ServiceTime
		c := t.Capacity
		if c <= 0 {
			c = 1
		}
		capacity[t.Name] = c
	}
	dispatched := make(map[string]int, len(targetsList))
	for _, r := range wr.Records {
		dispatched[r.Target]++
	}
	out := make(map[string]float64, len(targetsList))
	for name, count := range dispatched {
		lambda := float64(count) / horizon.Seconds()
		out[name] = lambda * svc[name].Seconds() / float64(capacity[name])
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===========================================================================")
	fmt.Println(" Experiment 013-B: Is Capacity=1 Special, or Is Normalized Rho What Matters?")
	fmt.Println(" (Section 21 -- scaling arrival rate proportionally with capacity)")
	fmt.Println("===========================================================================")

	baseRequests := 300 // at Capacity=1, matching Stage 12's own flagship exactly
	var cells []Cell
	for i, capacity := range []int{1, 2, 4, 8} {
		requestCount := baseRequests * capacity
		rootSeed := int64(13100 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requestCount, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("capacity=%d: generating traffic: %v", capacity, err)
		}
		effectiveRate := float64(requestCount) / horizon.Seconds()

		tgts := targets(capacity)
		scenario := replay.Scenario{Targets: tgts, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013b-cap%d", capacity), Scenario: scenario}

		fmt.Printf("\n-- Capacity=%d, Requests=%d (effective rate %.1f req/s) --\n", capacity, requestCount, effectiveRate)
		for i, ps := range policySpecs() {
			exp.Policy = ps.spec
			var result engine.RunResult
			var err error
			if i == 0 {
				result, err = v.Run(exp)
			} else {
				result, err = v.Replay(exp, ps.spec)
			}
			if err != nil {
				log.Fatalf("capacity=%d, %s: %v", capacity, ps.name, err)
			}
			wr := result.WorldResult
			ms := make([]float64, len(wr.Completions))
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			mean, _ := statistics.Mean(ms)
			maxCount := 0
			for _, c := range wr.CompletedByTarget {
				if c > maxCount {
					maxCount = c
				}
			}
			maxShare := 0.0
			if len(wr.Completions) > 0 {
				maxShare = float64(maxCount) / float64(len(wr.Completions))
			}
			rho := offeredRho(wr, tgts, horizon)
			maxRho := 0.0
			for _, r := range rho {
				if r > maxRho {
					maxRho = r
				}
			}
			cell := Cell{Capacity: capacity, Requests: requestCount, EffectiveRatePerS: effectiveRate, Policy: ps.name,
				MeanMs: mean, Completed: len(wr.Completions), MaxShare: maxShare, OfferedRhoMax: maxRho}
			cells = append(cells, cell)
			fmt.Printf("  %-10s completed=%-5d/%-5d mean=%9.2fms  max_share=%.3f  offered_rho_max=%.3f\n",
				ps.name, cell.Completed, requestCount, mean, maxShare, maxRho)
		}
	}

	fmt.Println("\n--- Does the winner track normalized rho, or the literal capacity number? ---")
	byCap := map[int]map[string]Cell{}
	for _, c := range cells {
		if byCap[c.Capacity] == nil {
			byCap[c.Capacity] = map[string]Cell{}
		}
		byCap[c.Capacity][c.Policy] = c
	}
	for _, capacity := range []int{1, 2, 4, 8} {
		e, a := byCap[capacity]["ewma"], byCap[capacity]["adaptive"]
		winner := "EWMA"
		if a.MeanMs < e.MeanMs {
			winner = "Adaptive"
		}
		fmt.Printf("Capacity=%-2d (rate=%6.0freq/s): ewma_offered_rho=%.3f  ewma_mean=%9.2fms  adaptive_mean=%9.2fms  -- %s wins\n",
			capacity, byCap[capacity]["ewma"].EffectiveRatePerS, e.OfferedRhoMax, e.MeanMs, a.MeanMs, winner)
	}
	fmt.Println("\nIf every cell shows a similar offered_rho_max (~1.0-1.1) AND Adaptive wins in every one,")
	fmt.Println("that is strong evidence normalized pressure -- not the literal 'Capacity=1' number -- is what matters.")

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
	}{Experiment: "013-B-capacity-1-artifact-test", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013B-capacity-1-artifact-test.json"), b, 0644)
	fmt.Println("\nExperiment 013-B complete.")
}
