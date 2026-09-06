// Command experiment-015e is Stage 15 Sections 23-25: does committed
// backlog remain a more informative severity predictor than peak rho or
// raw concentration (M1) across a compact set of representative
// topology and workload variations -- not a full Stage-14-style matrix,
// per this stage's own instruction to use only a few representative
// points.
//
// Part 1 (cross-topology, Section 24): reuses Stage 14's own N=3/5/8
// graduated near-boundary cells (experiment-014c's exact request counts)
// plus its N=8 bimodal cell (experiment-014b's 5-fast/3-slow config),
// running EWMA (the policy already established as concentration-prone)
// on each and comparing peak-rho, concentration, and committed backlog
// against actual severity (mean/p99).
//
// Part 2 (cross-workload, Section 25): the canonical N=5 topology under
// Constant, Burst, and FlashCrowd, matched total request count/horizon,
// same predictor comparison.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/015-mechanism-identification/results"
const congestionRatioThreshold = 1.0
const diversionWindow = 20
const diversionShareThreshold = 0.5

type PredictorRow struct {
	Scenario         string  `json:"scenario"`
	MeanMs           float64 `json:"mean_ms"`
	P99Ms            float64 `json:"p99_ms"`
	OfferedRhoMax    float64 `json:"offered_rho_max"`
	Top1Share        float64 `json:"top1_share"`
	EntropyBits      float64 `json:"entropy_bits"`
	PeakDepth        int     `json:"peak_depth"`
	CommittedBacklog int     `json:"committed_backlog"`
	DrainFound       bool    `json:"drain_found"`
}

func offeredRhoPerTarget(wr *replay.WorldResult, targetsList []replay.TargetProfile, horizonMs float64) map[string]float64 {
	svc := make(map[string]time.Duration, len(targetsList))
	capOf := make(map[string]int, len(targetsList))
	for _, t := range targetsList {
		svc[t.Name] = t.ServiceTime
		c := t.Capacity
		if c <= 0 {
			c = 1
		}
		capOf[t.Name] = c
	}
	dispatched := make(map[string]int, len(targetsList))
	for _, r := range wr.Records {
		dispatched[r.Target]++
	}
	out := make(map[string]float64, len(targetsList))
	for name, count := range dispatched {
		lambda := float64(count) / (horizonMs / 1000.0)
		out[name] = lambda * svc[name].Seconds() / float64(capOf[name])
	}
	return out
}

func analyzeRow(name string, wr *replay.WorldResult, targetsList []replay.TargetProfile, horizonMs float64) PredictorRow {
	r := PredictorRow{Scenario: name}
	if len(wr.Completions) > 0 {
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		r.MeanMs, _ = statistics.Mean(ms)
		r.P99Ms, _ = statistics.Percentile(ms, 99)
	}
	rho := offeredRhoPerTarget(wr, targetsList, horizonMs)
	for _, v := range rho {
		if v > r.OfferedRhoMax {
			r.OfferedRhoMax = v
		}
	}
	conc := backlog.ComputeConcentration(wr.CompletedByTarget)
	r.Top1Share, r.EntropyBits = conc.Top1Share, conc.EntropyBits

	bottleneck, bottleneckPeak, capOfBottleneck := "", -1, 1
	for _, t := range targetsList {
		tl := backlog.BuildTimeline(wr.Records, wr.Completions, t.Name)
		p := tl.PeakDepth()
		if p > bottleneckPeak {
			bottleneck, bottleneckPeak = t.Name, p
			c := t.Capacity
			if c <= 0 {
				c = 1
			}
			capOfBottleneck = c
		}
	}
	r.PeakDepth = bottleneckPeak
	tl := backlog.BuildTimeline(wr.Records, wr.Completions, bottleneck)
	onset, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capOfBottleneck, congestionRatioThreshold)
	cfg := backlog.CongestionConfig{RatioThreshold: congestionRatioThreshold, DiversionWindow: diversionWindow, DiversionShareThreshold: diversionShareThreshold}
	dr := backlog.AnalyzeDiversion(wr.Records, tl, bottleneck, capOfBottleneck, onset, onsetFound, cfg, horizonMs)
	r.CommittedBacklog, r.DrainFound = dr.CommittedBacklog, dr.QueueDrainFound
	return r
}

func graduatedTargets(n int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, n)
	for i := 0; i < n; i++ {
		out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(15*(i+1)) * time.Millisecond, Capacity: 1}
	}
	return out
}

func bimodalTargets(nFast, nSlow int) []replay.TargetProfile {
	out := make([]replay.TargetProfile, 0, nFast+nSlow)
	for i := 0; i < nFast; i++ {
		out = append(out, replay.TargetProfile{Name: fmt.Sprintf("fast-%02d", i), ServiceTime: 15 * time.Millisecond, Capacity: 1})
	}
	for i := 0; i < nSlow; i++ {
		out = append(out, replay.TargetProfile{Name: fmt.Sprintf("slow-%02d", i), ServiceTime: 60 * time.Millisecond, Capacity: 1})
	}
	return out
}

func runCrossTopology() []PredictorRow {
	fmt.Println("\n=== Part 1: Cross-Topology (Section 24) ===")
	horizon := 4 * time.Second
	configs := []struct {
		name     string
		targets  []replay.TargetProfile
		requests int
		seed     int64
	}{
		{"graduated_n3", graduatedTargets(3), 300, 14200 + 3},    // identical to experiment-014c's own N=3 cell
		{"graduated_n5", graduatedTargets(5), 336, 14200 + 5},    // identical to experiment-014c's own N=5 cell
		{"graduated_n8", graduatedTargets(8), 381, 14200 + 8},    // identical to experiment-014c's own N=8 cell
		{"bimodal_n8", bimodalTargets(5, 3), 300, 14100 + 2*100}, // identical to experiment-014b's own 5fast+3slow cell
	}
	var rows []PredictorRow
	for _, cfg := range configs {
		seeds := replay.DeriveSeeds(cfg.seed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: cfg.requests, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("%s: generating traffic: %v", cfg.name, err)
		}
		scenario := replay.Scenario{Targets: cfg.targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: "015e-topo-" + cfg.name, Scenario: scenario, Policy: replay.EWMAPolicy()}
		result, err := v.Run(exp)
		if err != nil {
			log.Fatalf("%s: %v", cfg.name, err)
		}
		row := analyzeRow(cfg.name, result.WorldResult, cfg.targets, float64(horizon.Milliseconds()))
		rows = append(rows, row)
		fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms  rho_max=%.3f  top1=%.3f  entropy=%.2f  peak_depth=%d  committed_backlog=%d  drain=%v\n",
			row.Scenario, row.MeanMs, row.P99Ms, row.OfferedRhoMax, row.Top1Share, row.EntropyBits, row.PeakDepth, row.CommittedBacklog, row.DrainFound)
	}
	return rows
}

func runCrossWorkload() []PredictorRow {
	fmt.Println("\n=== Part 2: Cross-Workload (Section 25) ===")
	horizon := 8 * time.Second
	targets := graduatedTargets(5)
	seeds := replay.DeriveSeeds(15500)

	var rows []PredictorRow
	for _, pattern := range []struct {
		name   string
		p      traffic.Pattern
		params traffic.Params
	}{
		{"constant", traffic.Constant, traffic.Params{Requests: 600, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)}},
		{"burst", traffic.Burst, traffic.Params{Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300, BurstAt: 4 * time.Second, BurstWidth: 1 * time.Second, KeyFunc: traffic.HotColdKeys(0.5)}},
		{"flash_crowd", traffic.FlashCrowd, traffic.Params{Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300, BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5)}},
	} {
		arrivals, err := traffic.Generate(pattern.p, pattern.params, seeds.Traffic)
		if err != nil {
			log.Fatalf("%s: generating traffic: %v", pattern.name, err)
		}
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: "015e-workload-" + pattern.name, Scenario: scenario, Policy: replay.EWMAPolicy()}
		result, err := v.Run(exp)
		if err != nil {
			log.Fatalf("%s: %v", pattern.name, err)
		}
		row := analyzeRow(pattern.name, result.WorldResult, targets, float64(horizon.Milliseconds()))
		rows = append(rows, row)
		fmt.Printf("  %-14s mean=%9.2fms  p99=%9.2fms  rho_max=%.3f  top1=%.3f  entropy=%.2f  peak_depth=%d  committed_backlog=%d  drain=%v\n",
			row.Scenario, row.MeanMs, row.P99Ms, row.OfferedRhoMax, row.Top1Share, row.EntropyBits, row.PeakDepth, row.CommittedBacklog, row.DrainFound)
	}
	return rows
}

// spearmanLikeRankCheck reports, informally, whether higher
// CommittedBacklog values line up with higher MeanMs values better than
// OfferedRhoMax does, across a small row set -- not a formal correlation
// coefficient (too few points for one to be meaningful), just an
// explicit, honest ranking comparison.
func rankAgreement(rows []PredictorRow, label string) {
	fmt.Printf("\n  -- %s: does committed backlog rank scenarios by severity better than peak rho? --\n", label)
	fmt.Println("  scenario        rank_by_mean  rank_by_rho  rank_by_backlog")
	type ranked struct {
		name        string
		meanRank    int
		rhoRank     int
		backlogRank int
	}
	n := len(rows)
	byMean := append([]PredictorRow{}, rows...)
	byRho := append([]PredictorRow{}, rows...)
	byBacklog := append([]PredictorRow{}, rows...)
	sortDesc := func(s []PredictorRow, less func(a, b PredictorRow) bool) {
		for i := 1; i < len(s); i++ {
			for j := i; j > 0 && less(s[j-1], s[j]); j-- {
				s[j-1], s[j] = s[j], s[j-1]
			}
		}
	}
	sortDesc(byMean, func(a, b PredictorRow) bool { return a.MeanMs < b.MeanMs })
	sortDesc(byRho, func(a, b PredictorRow) bool { return a.OfferedRhoMax < b.OfferedRhoMax })
	sortDesc(byBacklog, func(a, b PredictorRow) bool { return a.CommittedBacklog < b.CommittedBacklog })

	rankOf := func(sorted []PredictorRow, name string) int {
		for i, r := range sorted {
			if r.Scenario == name {
				return i + 1
			}
		}
		return -1
	}
	rhoDist, backlogDist := 0, 0
	for _, r := range rows {
		mr, rr, br := rankOf(byMean, r.Scenario), rankOf(byRho, r.Scenario), rankOf(byBacklog, r.Scenario)
		fmt.Printf("  %-14s %-13d %-12d %-16d\n", r.Scenario, mr, rr, br)
		diff := mr - rr
		if diff < 0 {
			diff = -diff
		}
		rhoDist += diff
		diff = mr - br
		if diff < 0 {
			diff = -diff
		}
		backlogDist += diff
	}
	fmt.Printf("  total rank distance from severity: peak_rho=%d  committed_backlog=%d  (lower is better; n=%d)\n", rhoDist, backlogDist, n)
	_ = ranked{}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-E: Predictor Generalization -- Cross-Topology and Cross-Workload")
	fmt.Println("=====================================================================================")

	topoRows := runCrossTopology()
	rankAgreement(topoRows, "cross-topology")

	workloadRows := runCrossWorkload()
	rankAgreement(workloadRows, "cross-workload")

	out := struct {
		Experiment    string         `json:"experiment"`
		Timestamp     string         `json:"timestamp"`
		CrossTopology []PredictorRow `json:"cross_topology"`
		CrossWorkload []PredictorRow `json:"cross_workload"`
	}{Experiment: "015-E-predictor-generalization", Timestamp: time.Now().UTC().Format(time.RFC3339), CrossTopology: topoRows, CrossWorkload: workloadRows}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015E-predictor-generalization.json"), b, 0644)
	fmt.Println("\nExperiment 015-E complete.")
}
