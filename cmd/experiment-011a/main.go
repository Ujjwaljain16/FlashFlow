// Command experiment-011a is Stage 11's Program A: a systematic policy
// regime map. 3 topology heterogeneity levels x 3 workload patterns x 3
// failure conditions = 27 scenario configurations, each run against all
// 6 routing policies through the IDENTICAL Scenario object (Run for the
// first policy, Replay for the rest) -- 162 controlled comparisons.
//
// This is a discovery tool, not a ranking machine: the goal is finding
// which conditions favor which policy, not crowning one policy the
// overall winner. Every number below comes from an actual RunWorld
// execution; the interpretation in docs/StageArtifacts/Stage11.md was
// written after reading this program's real output, not before.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/011-research-validation/results"

const (
	requests = 300
	horizon  = 4 * time.Second
)

type topologyLevel struct {
	name    string
	targets []replay.TargetProfile
}

func topologies() []topologyLevel {
	return []topologyLevel{
		{"homogeneous", []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 30 * time.Millisecond},
		}},
		{"moderate", []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 20 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 40 * time.Millisecond},
		}},
		{"severe", []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
		}},
	}
}

type workloadPattern struct {
	name   string
	params traffic.Params
	kind   traffic.Pattern
}

func workloads() []workloadPattern {
	return []workloadPattern{
		{"constant", traffic.Params{Requests: requests, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5)}, traffic.Constant},
		{"burst", traffic.Params{Requests: requests, Horizon: horizon, BaseRate: 40, PeakRate: 300, BurstAt: 2 * time.Second, BurstWidth: 800 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5)}, traffic.Burst},
		{"flash_crowd", traffic.Params{Requests: requests, Horizon: horizon, BaseRate: 40, PeakRate: 300, BurstAt: 2 * time.Second, BurstWidth: 800 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5)}, traffic.FlashCrowd},
	}
}

// failureConditions returns three chaos schedules against "edge-a" (the
// fastest target in every topology level above, kept fixed rather than
// varied so this stays a 3-factor, not 4-factor, matrix): no failure;
// an isolated failure away from any load transition (1.0s-1.5s); and a
// failure timed to overlap the burst/flash-crowd peak at 2s (1.8s-2.3s)
// -- the "failure during a load transition" condition Stage 11's brief
// names explicitly. For the constant-workload column there is no real
// "transition" to coincide with; the second condition still applies a
// differently-timed failure of the same duration, which is a legitimate
// (if narrower) comparison, noted as a limitation in Stage11.md rather
// than silently treated as equivalent to the burst/flash-crowd case.
func failureConditions() []struct {
	name     string
	schedule string
} {
	return []struct {
		name     string
		schedule string
	}{
		{"none", ""},
		{"isolated", "- at: 1s\n  target: edge-a\n  action: crash\n- at: 1.5s\n  target: edge-a\n  action: recover\n"},
		{"during_transition", "- at: 1.8s\n  target: edge-a\n  action: crash\n- at: 2.3s\n  target: edge-a\n  action: recover\n"},
	}
}

func policySpecs() []struct {
	name string
	spec replay.PolicySpec
} {
	return []struct {
		name string
		spec replay.PolicySpec
	}{
		{"round-robin", replay.RoundRobinPolicy()},
		{"weighted-round-robin", replay.WeightedRoundRobinPolicy()},
		{"least-connections", replay.LeastConnectionsPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"p2c-load", replay.P2CLoadPolicy()},
		{"adaptive", replay.AdaptivePolicy()},
	}
}

// RunMetrics is one (scenario config, policy) cell of the regime map.
type RunMetrics struct {
	Topology         string             `json:"topology"`
	Workload         string             `json:"workload"`
	Failure          string             `json:"failure"`
	Policy           string             `json:"policy"`
	MeanLatencyMs    float64            `json:"mean_latency_ms"`
	P95LatencyMs     float64            `json:"p95_latency_ms"`
	P99LatencyMs     float64            `json:"p99_latency_ms"`
	Completed        int                `json:"completed"`
	Rejected         int                `json:"rejected"`
	CompletionRate   float64            `json:"completion_rate"`
	MaxShare         float64            `json:"max_share"`         // largest single target's fraction of completions -- concentration/fairness signal
	MaxUtilization   float64            `json:"max_utilization"`   // max rho across targets, from internal/attribution
	FailedTargetUtil float64            `json:"failed_target_rho"` // edge-a's own rho, meaningful when a failure condition targets it
	Utilization      map[string]float64 `json:"utilization"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 011-A: Policy Regime Map")
	fmt.Println(" 3 topology levels x 3 workload patterns x 3 failure conditions x 6 policies = 162 runs")
	fmt.Println("==========================================================================================")

	var allMetrics []RunMetrics
	configIndex := 0

	for ti, topo := range topologies() {
		for wi, wl := range workloads() {
			for fi, fc := range failureConditions() {
				configIndex++
				rootSeed := int64(1000*ti + 100*wi + 10*fi + 1)
				seeds := replay.DeriveSeeds(rootSeed)

				arrivals, err := traffic.Generate(wl.kind, wl.params, seeds.Traffic)
				if err != nil {
					log.Fatalf("config %d (%s/%s/%s): generating traffic: %v", configIndex, topo.name, wl.name, fc.name, err)
				}

				var windows []replay.FailureWindow
				useHealth := false
				if fc.schedule != "" {
					sched, err := chaos.ParseYAML(strings.NewReader(fc.schedule))
					if err != nil {
						log.Fatalf("config %d: parsing chaos schedule: %v", configIndex, err)
					}
					windows, err = sched.ToFailureWindows()
					if err != nil {
						log.Fatalf("config %d: compiling chaos schedule: %v", configIndex, err)
					}
					useHealth = true
				}

				scenario := replay.Scenario{
					Targets:           topo.targets,
					Arrivals:          arrivals,
					Failures:          windows,
					UseHealthRegistry: useHealth,
					Horizon:           clock.VirtualTime(horizon.Nanoseconds()),
					Seeds:             seeds,
				}

				v := engine.NewVirtualEngine()
				exp := engine.Experiment{
					ID:       fmt.Sprintf("011a-%s-%s-%s", topo.name, wl.name, fc.name),
					Scenario: scenario,
				}

				var firstResult *replay.WorldResult
				for pi, ps := range policySpecs() {
					exp.Policy = ps.spec
					var result engine.RunResult
					var err error
					if pi == 0 {
						result, err = v.Run(exp)
					} else {
						result, err = v.Replay(exp, ps.spec)
					}
					if err != nil {
						log.Fatalf("config %d, policy %s: %v", configIndex, ps.name, err)
					}
					if pi == 0 {
						firstResult = result.WorldResult
					}
					_ = firstResult

					m := computeMetrics(topo.name, wl.name, fc.name, ps.name, result.WorldResult, topo.targets, horizon)
					allMetrics = append(allMetrics, m)
				}
			}
		}
	}

	fmt.Printf("\nCompleted %d runs across %d scenario configurations.\n", len(allMetrics), configIndex)

	summarizeRegimeMap(allMetrics)

	out := struct {
		Experiment string       `json:"experiment"`
		Timestamp  string       `json:"timestamp"`
		Runs       []RunMetrics `json:"runs"`
	}{
		Experiment: "011-A-policy-regime-map", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Runs: allMetrics,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011A-policy-regime-map.json"), b, 0644)

	fmt.Println("\nExperiment 011-A complete.")
}

func computeMetrics(topoName, wlName, fcName, policyName string, wr *replay.WorldResult, targets []replay.TargetProfile, horizonDur time.Duration) RunMetrics {
	m := RunMetrics{Topology: topoName, Workload: wlName, Failure: fcName, Policy: policyName}
	m.Completed = len(wr.Completions)
	m.Rejected = wr.RejectedCount
	total := m.Completed + m.Rejected
	if total > 0 {
		m.CompletionRate = float64(m.Completed) / float64(total)
	}
	if len(wr.Completions) > 0 {
		ms := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			ms[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		m.MeanLatencyMs, _ = statistics.Mean(ms)
		m.P95LatencyMs, _ = statistics.Percentile(ms, 95)
		m.P99LatencyMs, _ = statistics.Percentile(ms, 99)
	}
	maxCount := 0
	for _, c := range wr.CompletedByTarget {
		if c > maxCount {
			maxCount = c
		}
	}
	if m.Completed > 0 {
		m.MaxShare = float64(maxCount) / float64(m.Completed)
	}
	util, err := attribution.UtilizationFromWorld(*wr, targets, horizonDur)
	if err == nil {
		m.Utilization = util
		for _, t := range targets {
			if util[t.Name] > m.MaxUtilization {
				m.MaxUtilization = util[t.Name]
			}
		}
		m.FailedTargetUtil = util["edge-a"]
	}
	return m
}

// summarizeRegimeMap prints, per scenario configuration, which policy had
// the lowest mean latency and lowest p99 -- the raw material for finding
// regime boundaries, not a verdict. Full numbers are in the JSON artifact.
func summarizeRegimeMap(all []RunMetrics) {
	type key struct{ topo, wl, fc string }
	groups := map[key][]RunMetrics{}
	var order []key
	for _, m := range all {
		k := key{m.Topology, m.Workload, m.Failure}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}

	winsByPolicy := map[string]int{}
	fmt.Println("\nscenario                                        best-mean-latency      best-p99")
	fmt.Println(strings.Repeat("-", 95))
	for _, k := range order {
		runs := groups[k]
		sort.Slice(runs, func(i, j int) bool { return runs[i].MeanLatencyMs < runs[j].MeanLatencyMs })
		bestMean := runs[0]
		winsByPolicy[bestMean.Policy]++

		byP99 := append([]RunMetrics(nil), runs...)
		sort.Slice(byP99, func(i, j int) bool { return byP99[i].P99LatencyMs < byP99[j].P99LatencyMs })
		bestP99 := byP99[0]

		label := fmt.Sprintf("%s/%s/%s", k.topo, k.wl, k.fc)
		fmt.Printf("%-45s  %-18s(%.2fms)  %-14s(%.2fms)\n", label, bestMean.Policy, bestMean.MeanLatencyMs, bestP99.Policy, bestP99.P99LatencyMs)
	}

	fmt.Println("\nMean-latency wins by policy across 27 scenario configurations (not a ranking -- a regime-frequency count):")
	for _, ps := range policySpecs() {
		fmt.Printf("  %-22s %d/27\n", ps.name, winsByPolicy[ps.name])
	}
}
