// Command experiment-012c re-runs Stage 11 Program B's B1 scenario
// (cache-affinity deception) under Stage 12's contention model. The old
// question was "does cache affinity trap Adaptive?" (yes, confirmed
// causally, Stage11.md §12). The new question: does the trap remain
// harmful once the system can express capacity pressure -- or does
// forcing Adaptive to spread load as a side effect of avoiding capacity
// overload change how costly the trap actually is?
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

const outDirName = "experiments/012-model-fidelity/results"

type Result struct {
	Policy          string  `json:"policy"`
	Capacity        int     `json:"capacity"`
	MeanMs          float64 `json:"mean_ms"`
	PostRecoveryToA int     `json:"post_recovery_share_to_a_pct"`
}

func b1Policies() []struct {
	name string
	spec replay.PolicySpec
} {
	noCacheCfg := proxy.DefaultAdaptiveConfig()
	noCacheCfg.Weights.Cache = 0
	tunedCfg := proxy.AdaptiveConfig{
		Weights:          proxy.AdaptiveWeights{Load: 0.161, Latency: 0.568, Cache: 0.051, Cost: 0.220},
		ReferenceLatency: 192 * time.Millisecond, StaleAfter: 3740 * time.Millisecond,
	}
	return []struct {
		name string
		spec replay.PolicySpec
	}{
		{"round-robin", replay.RoundRobinPolicy()},
		{"ewma", replay.EWMAPolicy()},
		{"adaptive-default", replay.AdaptivePolicy()},
		{"adaptive-no-cache-weight", replay.AdaptivePolicyWithConfig(noCacheCfg)},
		{"adaptive-stage8-tuned", replay.AdaptivePolicyWithConfig(tunedCfg)},
	}
}

func runB1(capacity int) []Result {
	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(3011) // identical root seed to Stage 11's own B1
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("capacity=%d: generating traffic: %v", capacity, err)
	}
	sched, err := chaos.ParseYAML(strings.NewReader(
		"- at: 1s\n  target: edge-a\n  action: crash\n- at: 2s\n  target: edge-a\n  action: recover\n"))
	if err != nil {
		log.Fatalf("parsing chaos: %v", err)
	}
	windows, err := sched.ToFailureWindows()
	if err != nil {
		log.Fatalf("compiling chaos: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
		Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: fmt.Sprintf("012c-b1-capacity%d", capacity), Scenario: scenario}

	var out []Result
	fmt.Printf("\n-- Capacity=%d --\n", capacity)
	for i, ps := range b1Policies() {
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
		r := Result{Policy: ps.name, Capacity: capacity, MeanMs: mean, PostRecoveryToA: pct}
		out = append(out, r)
		fmt.Printf("  %-26s mean=%7.2fms  post-recovery-share-to-A=%d%% (of %d)\n", ps.name, mean, pct, postRecovery)
	}
	return out
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
	fmt.Println("=================================================================")
	fmt.Println(" Experiment 012-C: Cache-Affinity Trap (B1) Re-run Under Contention")
	fmt.Println("=================================================================")

	var all []Result
	all = append(all, runB1(0)...) // flat, matches Stage 11 exactly
	all = append(all, runB1(1)...) // contention-enabled

	out := struct {
		Experiment string   `json:"experiment"`
		Timestamp  string   `json:"timestamp"`
		Results    []Result `json:"results"`
	}{Experiment: "012-C-cache-affinity-trap-under-contention", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "012C-cache-affinity-trap-under-contention.json"), b, 0644)
	fmt.Println("\nExperiment 012-C complete.")
}
