// Command experiment-015d is Stage 15 Section 21: does the committed-
// backlog mechanism explain Stage 13's own unusual cache-affinity result
// (experiment-013h: at Capacity=1, higher cache weight produces WORSE
// interim latency but a HIGHER eventual return-to-recovered-target
// rate)?
//
// Hypothesis under test (stated before running, per this stage's own
// anti-fishing discipline): higher affinity weight delays diversion away
// from whichever target absorbs the hot key's traffic while the
// affinity target is down, letting more work commit there before the
// policy corrects, which is what produces the worse interim latency --
// the SAME committed-backlog mechanism experiment-015a/015b already
// established, not a separate cache-specific phenomenon.
//
// Reuses experiment-013h's own B1 scenario (edge-a crashes at t=1s,
// recovers at t=2s, cache weight varied) EXACTLY at Capacity=1, the one
// level Stage 13 found the effect real at.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/015-mechanism-identification/results"
const horizon = 4 * time.Second
const capacity = 1

func targets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond, Capacity: capacity},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond, Capacity: capacity},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond, Capacity: capacity},
	}
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

// absorbingTarget finds whichever non-crashed target received the most
// hot-key traffic during the crash window [1000,2000)ms -- the target
// whose own committed backlog this experiment's hypothesis is about.
func absorbingTarget(records []replay.SelectionRecord, hotKey string) string {
	counts := map[string]int{}
	for _, r := range records {
		if r.Key == hotKey && r.VirtualTimeMs >= 1000 && r.VirtualTimeMs < 2000 && r.Target != "edge-a" {
			counts[r.Target]++
		}
	}
	best, bestN := "", 0
	for t, n := range counts {
		if n > bestN {
			best, bestN = t, n
		}
	}
	return best
}

// peakDepthAfter returns the maximum depth reached at or after afterMs
// -- unlike Timeline.PeakDepth (which scans the whole run), this isolates
// the post-recovery window specifically, carrying over whatever depth
// already existed at afterMs rather than incorrectly resetting to zero.
func peakDepthAfter(tl backlog.Timeline, afterMs float64) int {
	depth, peak := 0, 0
	for _, e := range tl.Events {
		depth += e.Delta
		if e.TimeMs >= afterMs && depth > peak {
			peak = depth
		}
	}
	return peak
}

type Cell struct {
	CacheWeight              float64 `json:"cache_weight"`
	MeanMs                   float64 `json:"mean_ms"`
	PostRecoveryPct          int     `json:"post_recovery_return_pct"`
	AbsorbingTarget          string  `json:"absorbing_target"`
	AbsorbingPeakDep         int     `json:"absorbing_target_peak_depth"`
	CommittedBacklog         int     `json:"committed_backlog_on_absorbing_target"`
	DrainFound               bool    `json:"drain_found"`
	DrainAtMs                float64 `json:"drain_at_ms"`
	EdgeAPostRecoveryPeak    int     `json:"edge_a_post_recovery_peak_depth"` // does the effect actually live HERE instead (a rush back to edge-a after t=2000ms), not on the absorbing target during the crash?
	EdgeAPostRecoveryBacklog int     `json:"edge_a_post_recovery_committed_backlog"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("=====================================================================================")
	fmt.Println(" Experiment 015-D: Cache-Affinity Mechanism -- Is It Committed Backlog? (Section 21)")
	fmt.Println("=====================================================================================")

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
	cacheWeights := []float64{0.02, 0.1, 0.3}
	for i, cacheWeight := range cacheWeights {
		cfg := proxy.DefaultAdaptiveConfig()
		cfg.Weights.Cache = cacheWeight
		rootSeed := int64(13800 + i + 1) // identical seeds to 013h's own cap=1 cells, for direct comparability
		seeds := replay.DeriveSeeds(rootSeed)
		arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
			Requests: 300, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5),
		}, seeds.Traffic)
		if err != nil {
			log.Fatalf("cache=%.2f: generating traffic: %v", cacheWeight, err)
		}
		scenario := replay.Scenario{
			Targets: targets(), Arrivals: arrivals, Failures: windows, UseHealthRegistry: true,
			Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
		}
		v := engine.NewVirtualEngine()
		exp := engine.Experiment{ID: fmt.Sprintf("015d-cache%.2f", cacheWeight), Scenario: scenario, Policy: replay.AdaptivePolicyWithConfig(cfg)}
		result, err := v.Run(exp)
		if err != nil {
			log.Fatalf("cache=%.2f: %v", cacheWeight, err)
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

		absorb := absorbingTarget(wr.Records, hotKey)
		cell := Cell{CacheWeight: cacheWeight, MeanMs: mean, PostRecoveryPct: pct, AbsorbingTarget: absorb}
		if absorb != "" {
			tl := backlog.BuildTimeline(wr.Records, wr.Completions, absorb)
			cell.AbsorbingPeakDep = tl.PeakDepth()
			onset, onsetFound := backlog.FindFirstCongestionOnset(tl, capacity, 1.0, 1000) // only from the crash window onward
			dr := backlog.AnalyzeDiversion(wr.Records, tl, absorb, capacity, onset, onsetFound,
				backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 0.5}, float64(horizon.Milliseconds()))
			cell.CommittedBacklog, cell.DrainFound, cell.DrainAtMs = dr.CommittedBacklog, dr.QueueDrainFound, dr.QueueDrainAtMs
		}

		// Check the alternative hypothesis: does the interim-latency
		// effect actually live in a POST-recovery rush back to edge-a
		// (a stronger cache pull causing more traffic to return to edge-a
		// per unit time than it can serve at Capacity=1), not in
		// during-crash backlog on the absorbing target?
		edgeATL := backlog.BuildTimeline(wr.Records, wr.Completions, "edge-a")
		aOnset, aOnsetFound := backlog.FindFirstCongestionOnset(edgeATL, capacity, 1.0, 2000)
		aDR := backlog.AnalyzeDiversion(wr.Records, edgeATL, "edge-a", capacity, aOnset, aOnsetFound,
			backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 0.5}, float64(horizon.Milliseconds()))
		cell.EdgeAPostRecoveryPeak = peakDepthAfter(edgeATL, 2000)
		cell.EdgeAPostRecoveryBacklog = aDR.CommittedBacklog

		cells = append(cells, cell)
		fmt.Printf("  cache_weight=%.2f  mean=%9.2fms  post_recovery_return=%d%%  absorbing_target=%s  peak_depth=%d  committed_backlog=%d  drain=%v(@%.0fms)  edgeA_post_recovery_peak=%d  edgeA_post_recovery_backlog=%d\n",
			cacheWeight, mean, pct, absorb, cell.AbsorbingPeakDep, cell.CommittedBacklog, cell.DrainFound, cell.DrainAtMs, cell.EdgeAPostRecoveryPeak, cell.EdgeAPostRecoveryBacklog)
	}

	fmt.Println("\n--- Does higher cache weight track with MORE committed backlog on the absorbing target? ---")
	monotonicBacklog, monotonicMean := true, true
	for i := 1; i < len(cells); i++ {
		if cells[i].CommittedBacklog < cells[i-1].CommittedBacklog {
			monotonicBacklog = false
		}
		if cells[i].MeanMs < cells[i-1].MeanMs {
			monotonicMean = false
		}
	}
	fmt.Printf("  committed_backlog (absorbing target, during crash) increases monotonically with cache weight: %v\n", monotonicBacklog)
	fmt.Printf("  mean latency increases monotonically with cache weight: %v (Stage 13's own 013h finding)\n", monotonicMean)
	monotonicEdgeAPeak, monotonicEdgeABacklog := true, true
	for i := 1; i < len(cells); i++ {
		if cells[i].EdgeAPostRecoveryPeak < cells[i-1].EdgeAPostRecoveryPeak {
			monotonicEdgeAPeak = false
		}
		if cells[i].EdgeAPostRecoveryBacklog < cells[i-1].EdgeAPostRecoveryBacklog {
			monotonicEdgeABacklog = false
		}
	}
	fmt.Printf("  edge-a's own POST-RECOVERY peak depth increases monotonically with cache weight: %v\n", monotonicEdgeAPeak)
	fmt.Printf("  edge-a's own POST-RECOVERY committed backlog increases monotonically with cache weight: %v\n", monotonicEdgeABacklog)

	switch {
	case monotonicBacklog && monotonicMean:
		fmt.Println("  RESULT: SUPPORTS the ORIGINAL hypothesis -- higher cache weight tracks with more committed backlog")
		fmt.Println("  on the absorbing target during the crash, consistent with the committed-backlog mechanism.")
	case monotonicEdgeAPeak || monotonicEdgeABacklog:
		fmt.Println("  RESULT: the ORIGINAL hypothesis (backlog on the absorbing target DURING the crash) is FALSIFIED --")
		fmt.Println("  committed backlog there does not track with cache weight. But the effect DOES appear post-recovery,")
		fmt.Println("  on edge-a itself: a stronger cache pull causes a sharper RUSH BACK to the just-recovered target,")
		fmt.Println("  which at Capacity=1 creates its own committed backlog -- the SAME underlying mechanism (committed")
		fmt.Println("  backlog explains worse interim latency), just located at a different point in the timeline than")
		fmt.Println("  originally hypothesized. Refine, don't discard: it's committed backlog, but on RETURN, not on absorb.")
	default:
		fmt.Println("  RESULT: neither the original nor the refined hypothesis holds cleanly -- report as genuinely unresolved,")
		fmt.Println("  not force-fit to the committed-backlog mechanism.")
	}

	out := struct {
		Experiment string `json:"experiment"`
		Timestamp  string `json:"timestamp"`
		Cells      []Cell `json:"cells"`
	}{Experiment: "015-D-cache-affinity-committed-backlog", Timestamp: time.Now().UTC().Format(time.RFC3339), Cells: cells}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "015D-cache-affinity-committed-backlog.json"), b, 0644)
	fmt.Println("\nExperiment 015-D complete.")
}
