// Command experiment-013c is Stage 13 Program B: fix topology AND
// capacity, vary only arrival rate (via Requests, since Constant ignores
// BaseRate -- see experiment-013b's own confirmed finding), and ask
// whether the EWMA/Adaptive transition tracks the resulting (correctly
// capacity-normalized) rho or the raw arrival rate number directly.
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
const fixedCapacity = 2 // chosen because experiment-013a found EWMA fully recovers here at the ORIGINAL 75req/s rate -- if rho is what matters, high enough arrival rate at this SAME fixed capacity should push it back into collapse

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: fixedCapacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: fixedCapacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: fixedCapacity},
	}
}

type Cell struct {
	RequestCount  int     `json:"request_count"`
	RatePerS      float64 `json:"rate_per_sec"`
	Policy        string  `json:"policy"`
	MeanMs        float64 `json:"mean_ms"`
	MaxShare      float64 `json:"max_share"`
	OfferedRhoMax float64 `json:"offered_rho_max"` // capacity-normalized, per experiment-013b's corrected formula
}

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

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================")
	fmt.Println(" Experiment 013-C: Arrival-Rate Sweep at Fixed Topology/Capacity")
	fmt.Printf(" (severe heterogeneity, Capacity=%d fixed, only arrival rate varies)\n", fixedCapacity)
	fmt.Println("=====================================================================")

	// Request counts chosen to sweep offered rho roughly from well under 1
	// to well over 1 at this fixed capacity: 300 (matches Stage 12's own
	// original rate), then increasing.
	var cells []Cell
	for i, requestCount := range []int{150, 300, 450, 600, 750, 900, 1200} {
		rootSeed := int64(13200 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requestCount, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("requests=%d: generating traffic: %v", requestCount, err)
		}
		rate := float64(requestCount) / horizon.Seconds()

		tgts := targets()
		scenario := replay.Scenario{Targets: tgts, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013c-requests%d", requestCount), Scenario: scenario}

		fmt.Printf("\n-- Requests=%d (rate=%.1f req/s) --\n", requestCount, rate)
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
				log.Fatalf("requests=%d, %s: %v", requestCount, ps.name, err)
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
			cell := Cell{RequestCount: requestCount, RatePerS: rate, Policy: ps.name, MeanMs: mean, MaxShare: maxShare, OfferedRhoMax: maxRho}
			cells = append(cells, cell)
			fmt.Printf("  %-10s mean=%9.2fms  max_share=%.3f  offered_rho_max=%.3f\n", ps.name, mean, maxShare, maxRho)
		}
	}

	fmt.Println("\n--- Transition point ---")
	byReq := map[int]map[string]Cell{}
	for _, c := range cells {
		if byReq[c.RequestCount] == nil {
			byReq[c.RequestCount] = map[string]Cell{}
		}
		byReq[c.RequestCount][c.Policy] = c
	}
	for _, requestCount := range []int{150, 300, 450, 600, 750, 900, 1200} {
		e, a := byReq[requestCount]["ewma"], byReq[requestCount]["adaptive"]
		winner := "EWMA"
		if a.MeanMs < e.MeanMs {
			winner = "Adaptive"
		}
		fmt.Printf("rate=%6.0freq/s  ewma_rho=%.3f  ewma_mean=%9.2fms  adaptive_mean=%9.2fms  -- %s wins\n",
			e.RatePerS, e.OfferedRhoMax, e.MeanMs, a.MeanMs, winner)
	}

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
	}{Experiment: "013-C-arrival-rate-sweep", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013C-arrival-rate-sweep.json"), b, 0644)
	fmt.Println("\nExperiment 013-C complete.")
}
