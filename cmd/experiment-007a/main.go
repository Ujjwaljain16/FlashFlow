package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
)

const outDirName = "experiments/007-adaptive-replay/results"

func req(path string) *http.Request { return httptest.NewRequest(http.MethodGet, path, nil) }

type CheckResult struct {
	Name     string `json:"name"`
	Pass     bool   `json:"pass"`
	Detail   string `json:"detail"`
	Findings string `json:"findings"`
}

func scoreFor(sel *proxy.AdaptiveSelector, target string, others []string) float64 {
	all := append([]string{target}, others...)
	for _, s := range sel.Explain(req("/probe"), all) {
		if s.Target == target {
			return s.CombinedScore
		}
	}
	return 0
}

func isNonIncreasing(values []float64) bool {
	for i := 1; i < len(values); i++ {
		if values[i] > values[i-1]+1e-9 {
			return false
		}
	}
	return true
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 007-A: Adaptive Signal Validation")
	fmt.Println(" Does each individual signal behave monotonically, and do cold-start/staleness behave")
	fmt.Println(" exactly as designed?")
	fmt.Println("==========================================================================================")

	var results []CheckResult
	record := func(r CheckResult) {
		results = append(results, r)
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
		}
		fmt.Printf("\n[%s] %s\n  %s\n", status, r.Name, r.Detail)
	}
	allPass := true

	// Check 1: Load monotonicity -- increasing load must not increase score.
	{
		var scores []float64
		loadLevels := []int{0, 1, 2, 5, 10, 20}
		for _, level := range loadLevels {
			// A fresh tracker/selector per level for a clean, independent
			// reading at each load value.
			lt := proxy.NewLoadTracker()
			for j := 0; j < level; j++ {
				lt.Increment("probe")
			}
			sel := proxy.NewAdaptiveSelector(lt, proxy.NewLatencyTracker(0.2), nil, nil, nil, proxy.DefaultAdaptiveConfig())
			scores = append(scores, scoreFor(sel, "probe", []string{"other"}))
		}
		pass := isNonIncreasing(scores)
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "load_monotonicity", Pass: pass,
			Detail:   fmt.Sprintf("load levels %v -> scores %v (want non-increasing)", loadLevels, scores),
			Findings: "Increasing load (relative to fixed capacity) must never increase a target's score.",
		})
	}

	// Check 2: Latency monotonicity -- increasing latency must not increase score.
	{
		latencies := []time.Duration{1 * time.Millisecond, 10 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second}
		var scores []float64
		for _, lat := range latencies {
			lt := proxy.NewLatencyTracker(0.2)
			lt.Observe("probe", lat)
			mc := clock.NewMockClock(1000)
			// A fresh selector has no lastSelected record for "probe" yet,
			// so its latency estimate is trusted as-is (no staleness
			// evidence exists to discount it) -- see adaptive.go's design.
			sel := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), lt, nil, nil, mc, proxy.DefaultAdaptiveConfig())
			scores = append(scores, scoreFor(sel, "probe", []string{"other"}))
		}
		pass := isNonIncreasing(scores)
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "latency_monotonicity", Pass: pass,
			Detail:   fmt.Sprintf("latencies %v -> scores %v (want non-increasing)", latencies, scores),
			Findings: "Increasing observed latency must never increase a target's score.",
		})
	}

	// Check 3: Cost monotonicity -- increasing configured cost must not increase score.
	{
		costs := []int{1, 2, 5, 10, 20}
		var scores []float64
		for _, c := range costs {
			cost := proxy.TargetWeights{"probe": c, "other": 1}
			sel := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), proxy.NewLatencyTracker(0.2), nil, cost, nil, proxy.DefaultAdaptiveConfig())
			scores = append(scores, scoreFor(sel, "probe", []string{"other"}))
		}
		pass := isNonIncreasing(scores)
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "cost_monotonicity", Pass: pass,
			Detail:   fmt.Sprintf("costs %v -> scores %v (want non-increasing)", costs, scores),
			Findings: "Increasing a target's configured cost, relative to a fixed rival, must never increase its score.",
		})
	}

	// Check 4: Cold-start neutrality -- unobserved sits strictly between a
	// good observed target and a bad observed target, at exactly 0.5.
	{
		latGood := proxy.NewLatencyTracker(0.2)
		latGood.Observe("good", 1*time.Millisecond)
		selGood := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), latGood, nil, nil, nil, proxy.DefaultAdaptiveConfig())
		goodScore := selGood.Explain(req("/a"), []string{"good"})[0].LatencyScore

		latBad := proxy.NewLatencyTracker(0.2)
		latBad.Observe("bad", 5*time.Second)
		selBad := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), latBad, nil, nil, nil, proxy.DefaultAdaptiveConfig())
		badScore := selBad.Explain(req("/a"), []string{"bad"})[0].LatencyScore

		selCold := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), proxy.NewLatencyTracker(0.2), nil, nil, nil, proxy.DefaultAdaptiveConfig())
		coldScore := selCold.Explain(req("/a"), []string{"never-observed"})[0].LatencyScore

		pass := coldScore == 0.5 && badScore < coldScore && coldScore < goodScore
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "cold_start_neutrality", Pass: pass,
			Detail: fmt.Sprintf("bad=%.3f cold=%.3f good=%.3f (want bad < cold == 0.5 < good)", badScore, coldScore, goodScore),
			Findings: "An unobserved target must score exactly neutral (0.5) on latency -- worse than a good observed " +
				"target, better than a bad observed one, never automatically winning or losing the way EWMA's " +
				"optimistic cold-start rule (proven to cause lock-in in Experiment 006-B) would.",
		})
	}

	// Check 5: Staleness collapses to neutral regardless of the
	// underlying (untrusted) estimate's quality.
	{
		cfg := proxy.DefaultAdaptiveConfig()
		cfg.StaleAfter = 100 * time.Millisecond

		latExcellent := proxy.NewLatencyTracker(0.2)
		latExcellent.Observe("stale-excellent", 1*time.Millisecond) // would score ~1 if trusted
		mc1 := clock.NewMockClock(0)
		sel1 := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), latExcellent, nil, nil, mc1, cfg)
		sel1.SelectTarget(req("/a"), []string{"stale-excellent"}) // records lastSelected at t=0
		mc1.Advance(200 * time.Millisecond)                       // now well past StaleAfter
		staleExcellentScore := sel1.Explain(req("/a"), []string{"stale-excellent"})[0].LatencyScore

		latTerrible := proxy.NewLatencyTracker(0.2)
		latTerrible.Observe("stale-terrible", 5*time.Second) // would score ~0 if trusted
		mc2 := clock.NewMockClock(0)
		sel2 := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), latTerrible, nil, nil, mc2, cfg)
		sel2.SelectTarget(req("/a"), []string{"stale-terrible"})
		mc2.Advance(200 * time.Millisecond)
		staleTerribleScore := sel2.Explain(req("/a"), []string{"stale-terrible"})[0].LatencyScore

		pass := staleExcellentScore == 0.5 && staleTerribleScore == 0.5
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "staleness_collapses_to_neutral", Pass: pass,
			Detail: fmt.Sprintf("stale-but-excellent=%.3f, stale-but-terrible=%.3f (want both == 0.5)", staleExcellentScore, staleTerribleScore),
			Findings: "Once a target's data is stale, its score must fall back to neutral regardless of whether the " +
				"untrusted underlying estimate was good or bad -- old information is never treated as current truth, " +
				"the direct fix for the staleness blind spot Stage 3/6 demonstrated in EWMA.",
		})
	}

	// Check 6: Cache affinity is exactly 0 or 1, and only for the
	// correct (target, key) pair.
	{
		sel := proxy.NewAdaptiveSelector(proxy.NewLoadTracker(), proxy.NewLatencyTracker(0.2), nil, nil, nil, proxy.DefaultAdaptiveConfig())
		sel.SelectTarget(req("/key-x"), []string{"edge-a", "edge-b"}) // establishes affinity for whichever wins (edge-a, deterministic tie-break)

		scoresForKeyX := sel.Explain(req("/key-x"), []string{"edge-a", "edge-b"})
		scoresForKeyY := sel.Explain(req("/key-y"), []string{"edge-a", "edge-b"}) // different key -- no affinity established yet

		var edgeAonX, edgeBonX, edgeAonY float64
		for _, s := range scoresForKeyX {
			if s.Target == "edge-a" {
				edgeAonX = s.CacheScore
			}
			if s.Target == "edge-b" {
				edgeBonX = s.CacheScore
			}
		}
		for _, s := range scoresForKeyY {
			if s.Target == "edge-a" {
				edgeAonY = s.CacheScore
			}
		}

		pass := edgeAonX == 1 && edgeBonX == 0 && edgeAonY == 0
		if !pass {
			allPass = false
		}
		record(CheckResult{
			Name: "cache_affinity_specificity", Pass: pass,
			Detail: fmt.Sprintf("edge-a on key-x=%.0f (want 1), edge-b on key-x=%.0f (want 0), edge-a on key-y=%.0f (want 0)",
				edgeAonX, edgeBonX, edgeAonY),
			Findings: "Cache affinity must contribute only for the exact target that last served the exact key -- " +
				"never for a different target on the same key, never for the same target on a different key.",
		})
	}

	fmt.Printf("\n--- Summary: %d/%d checks passed ---\n", countPass(results), len(results))

	out := struct {
		Experiment string        `json:"experiment"`
		Timestamp  string        `json:"timestamp"`
		AllPass    bool          `json:"all_pass"`
		Checks     []CheckResult `json:"checks"`
	}{
		Experiment: "007-A-adaptive-signal-validation", Timestamp: time.Now().UTC().Format(time.RFC3339),
		AllPass: allPass, Checks: results,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "007A-adaptive-signal-validation.json"), b, 0644)

	if !allPass {
		log.Fatal("experiment 007-A failed: one or more signal validation checks did not behave as expected")
	}

	fmt.Println("\nExperiment 007-A complete.")
}

func countPass(results []CheckResult) int {
	n := 0
	for _, r := range results {
		if r.Pass {
			n++
		}
	}
	return n
}
