// Command experiment-013d covers two related Stage 13 questions:
//
// Part 1 (Section 22, service-time scaling): scale ALL service times AND
// the horizon by the same factor k, holding Requests fixed -- this keeps
// rho EXACTLY constant (rate scales by 1/k, service time scales by k,
// rho = rate*serviceTime/capacity is k-invariant) while absolute latency
// scale changes k-fold. If the regime (winner, and latency scaled back
// down by k) matches across k=1/2/4, that is direct evidence rho -- not
// absolute timescale -- is what matters.
//
// Part 2 (Program C's specific ratio question): fix the fastest target's
// absolute service time (which experiment-013a found is what actually
// determines whether a given capacity/rate combination crosses rho=1),
// and vary ONLY how much slower the other two targets are. If EWMA's
// concentration share and the resulting max-target rho stay similar
// regardless of how extreme the OTHER targets are, that confirms
// heterogeneity RATIO (independent of the fastest target's own absolute
// speed) is not the primary driver -- consistent with 013a's own finding.
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

type ScaleCell struct {
	ScaleFactor   int     `json:"scale_factor"`
	Policy        string  `json:"policy"`
	MeanMs        float64 `json:"mean_ms"`
	MeanMsScaled  float64 `json:"mean_ms_scaled_back"` // MeanMs / ScaleFactor -- should match k=1's MeanMs if scale-invariant
	OfferedRhoMax float64 `json:"offered_rho_max"`
}

func runScalePart() []ScaleCell {
	fmt.Println("\n=== Part 1: Service-Time + Horizon Scaling (rho held constant) ===")
	var cells []ScaleCell
	const requests = 300
	for i, k := range []int{1, 2, 4} {
		horizon := time.Duration(4*k) * time.Second
		targets := []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: time.Duration(15*k) * time.Millisecond, Capacity: 1},
			{Name: "edge-b", ServiceTime: time.Duration(30*k) * time.Millisecond, Capacity: 1},
			{Name: "edge-c", ServiceTime: time.Duration(60*k) * time.Millisecond, Capacity: 1},
		}
		rootSeed := int64(13300 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("scale=%d: generating traffic: %v", k, err)
		}
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013d-scale%d", k), Scenario: scenario}

		fmt.Printf("\n-- scale=%dx (service times %dx, horizon %dx, rate scales 1/%dx, rho held constant) --\n", k, k, k, k)
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
				log.Fatalf("scale=%d, %s: %v", k, ps.name, err)
			}
			wr := result.WorldResult
			ms := make([]float64, len(wr.Completions))
			for i, c := range wr.Completions {
				ms[i] = float64(c.Latency.Microseconds()) / 1000.0
			}
			mean, _ := statistics.Mean(ms)
			rho := offeredRho(wr, targets, horizon)
			maxRho := 0.0
			for _, r := range rho {
				if r > maxRho {
					maxRho = r
				}
			}
			cell := ScaleCell{ScaleFactor: k, Policy: ps.name, MeanMs: mean, MeanMsScaled: mean / float64(k), OfferedRhoMax: maxRho}
			cells = append(cells, cell)
			fmt.Printf("  %-10s mean=%9.2fms  mean/scale=%9.2fms  offered_rho_max=%.3f\n", ps.name, mean, cell.MeanMsScaled, maxRho)
		}
	}
	return cells
}

type RatioCell struct {
	OtherTargetsMultiplier float64 `json:"other_targets_multiplier"`
	Policy                 string  `json:"policy"`
	MeanMs                 float64 `json:"mean_ms"`
	MaxShare               float64 `json:"max_share"`
	OfferedRhoFastest      float64 `json:"offered_rho_fastest_target"`
}

func runRatioPart() []RatioCell {
	fmt.Println("\n=== Part 2: Heterogeneity RATIO, Fastest Target's Absolute Speed Held Fixed ===")
	var cells []RatioCell
	const requests = 300
	const horizon = 4 * time.Second
	// edge-a is ALWAYS 15ms; edge-b/edge-c are m*15ms and m*30ms respectively
	// -- m=1 means all three targets are IDENTICAL (homogeneous); increasing
	// m widens the gap without ever changing edge-a's own absolute speed.
	for i, m := range []float64{1.0, 1.5, 2.0, 4.0} {
		targets := []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: 1},
			{Name: "edge-b", ServiceTime: time.Duration(15 * m * float64(time.Millisecond))},
			{Name: "edge-c", ServiceTime: time.Duration(30 * m * float64(time.Millisecond))},
		}
		targets[1].Capacity = 1
		targets[2].Capacity = 1
		rootSeed := int64(13400 + i)
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("m=%.1f: generating traffic: %v", m, err)
		}
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("013d-ratio%.1f", m), Scenario: scenario}

		fmt.Printf("\n-- multiplier=%.1fx (targets: 15ms / %.1fms / %.1fms) --\n", m, 15*m, 30*m)
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
				log.Fatalf("m=%.1f, %s: %v", m, ps.name, err)
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
			rho := offeredRho(wr, targets, horizon)
			cell := RatioCell{OtherTargetsMultiplier: m, Policy: ps.name, MeanMs: mean, MaxShare: maxShare, OfferedRhoFastest: rho["edge-a"]}
			cells = append(cells, cell)
			fmt.Printf("  %-10s mean=%9.2fms  max_share=%.3f  offered_rho_edge_a=%.3f\n", ps.name, mean, maxShare, rho["edge-a"])
		}
	}
	return cells
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("======================================================================")
	fmt.Println(" Experiment 013-D: Service-Time Scaling + Heterogeneity Ratio")
	fmt.Println("======================================================================")

	scaleCells := runScalePart()
	ratioCells := runRatioPart()

	out := struct {
		Experiment string      `json:"experiment"`
		Timestamp  string      `json:"timestamp"`
		ScaleCells []ScaleCell `json:"scale_cells"`
		RatioCells []RatioCell `json:"ratio_cells"`
	}{Experiment: "013-D-scaling-and-ratio", Timestamp: time.Now().UTC().Format(time.RFC3339), ScaleCells: scaleCells, RatioCells: ratioCells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "013D-scaling-and-ratio.json"), b, 0644)
	fmt.Println("\nExperiment 013-D complete.")
}
