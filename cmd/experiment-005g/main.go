package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

// virtualPlaceholderRequest -- see the identical comment in
// cmd/experiment-005e/main.go: no selector reads this parameter.
var virtualPlaceholderRequest = &http.Request{}

type targetProfile struct {
	name        string
	serviceTime time.Duration
}

// ScenarioResult is everything about one run worth comparing across
// repeated executions, aside from the trace itself (compared separately
// since it's the strongest, most granular determinism check).
type ScenarioResult struct {
	CacheStats        cache.Stats             `json:"cache_stats"`
	HealthFinalStates map[string]health.State `json:"health_final_states"`
	CompletedByTarget map[string]int          `json:"completed_by_target"`
	RejectedCount     int                     `json:"rejected_count"`
	CacheHits         int                     `json:"cache_hits"`
	CacheMisses       int                     `json:"cache_misses"`
}

// runScenario composes only mechanisms already individually proven by
// 005-C (cache), 005-D (failure/recovery via health.Registry), and 005-E
// (stateful routing) -- the one new integration point is that routing's
// `available` list is filtered by registry.IsAvailable, gated on nothing
// but the probe schedule the same way the real system's Proxy+Checker+
// Registry combination is. No coalescing is exercised here: multiple
// misses for the same key arriving before the first one completes will
// each independently dispatch and each independently fill the cache on
// completion, exactly like the pre-004-D edge. That's deliberate scope,
// not an oversight -- see the README.
func runScenario() (*vtime.Engine, ScenarioResult) {
	e := vtime.NewEngine(0)

	profiles := []targetProfile{
		{"edge-a-slow", 100 * time.Millisecond},
		{"edge-b-fast", 20 * time.Millisecond},
		{"edge-c-fast", 20 * time.Millisecond},
	}
	allTargets := make([]string, len(profiles))
	serviceTimes := make(map[string]time.Duration, len(profiles))
	for i, p := range profiles {
		allTargets[i] = p.name
		serviceTimes[p.name] = p.serviceTime
	}

	// TTL deliberately long enough to never expire within this scenario's
	// ~1.5s traffic window -- expiry under virtual time is 005-C's proof,
	// not this one's.
	c := cache.New(e.Clock(), 10*time.Second)
	loadTracker := proxy.NewLoadTracker()
	latencyTracker := proxy.NewLatencyTracker(0.2)
	selector := proxy.NewLeastConnectionsSelector(loadTracker)
	registry := health.NewRegistry(e.Clock(), health.DefaultConfig())
	for _, t := range allTargets {
		registry.RegisterTarget(t)
	}

	// Ground truth, deliberately separate from the registry's observed
	// state -- the registry only ever learns about the world through
	// probe results, same design as 005-D.
	up := map[string]bool{"edge-a-slow": true, "edge-b-fast": true, "edge-c-fast": true}
	const failTarget = "edge-b-fast"
	e.Schedule(clock.VirtualTime(500*time.Millisecond), func() { up[failTarget] = false })
	e.Schedule(clock.VirtualTime(1000*time.Millisecond), func() { up[failTarget] = true })

	// Runs independently of the traffic mechanism below, on the same
	// timeline -- detection lag is an emergent property of this
	// interval and the registry's thresholds, not something routing can
	// see around.
	const probeInterval = 100 * time.Millisecond
	if _, err := e.NewTicker(0, probeInterval, func() {
		for _, t := range allTargets {
			state := registry.RecordProbeResult(t, up[t])
			e.Record("health_probe", t, map[string]any{"success": up[t], "state": string(state)})
		}
	}); err != nil {
		log.Fatalf("failed to start probe ticker: %v", err)
	}

	completedByTarget := make(map[string]int)
	rejectedCount := 0
	cacheHits, cacheMisses := 0, 0

	const requests = 300
	const spacing = 5 * time.Millisecond
	for i := 0; i < requests; i++ {
		reqID := fmt.Sprintf("r%d", i)
		var key string
		if i%2 == 0 {
			key = "hot"
		} else {
			key = fmt.Sprintf("cold-%d", i%3)
		}

		at := clock.VirtualTime(spacing.Nanoseconds() * int64(i))
		e.Schedule(at, func() {
			if _, ok := c.Get(key); ok {
				cacheHits++
				e.Record("cache_hit", reqID, map[string]any{"key": key})
				return
			}
			cacheMisses++
			e.Record("cache_miss", reqID, map[string]any{"key": key})

			var available []string
			for _, t := range allTargets {
				if registry.IsAvailable(t) {
					available = append(available, t)
				}
			}
			if len(available) == 0 {
				rejectedCount++
				e.Record("request_rejected", reqID, map[string]any{"key": key, "reason": "no_healthy_targets"})
				return
			}

			target, err := selector.SelectTarget(virtualPlaceholderRequest, available)
			if err != nil {
				log.Fatalf("unexpected selection error with %d available targets: %v", len(available), err)
			}
			e.Record("request_routed", reqID, map[string]any{"key": key, "target": target})
			loadTracker.Increment(target)
			arrival := e.Now()
			svc := serviceTimes[target]
			e.Schedule(arrival.Add(svc), func() {
				loadTracker.Decrement(target)
				latency := e.Now().Sub(arrival)
				latencyTracker.Observe(target, latency)
				c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v"), StoredAt: e.Now()})
				completedByTarget[target]++
				e.Record("request_completed", reqID, map[string]any{
					"key": key, "target": target, "latency_ms": float64(latency.Microseconds()) / 1000.0,
				})
			})
		})
	}

	// A fixed horizon, not RunUntilEmpty: the probe Ticker never stops
	// itself and would keep rescheduling forever otherwise. 2s is
	// comfortably past the latest possible completion (last arrival at
	// 1495ms + the slow target's 100ms service time).
	if err := e.RunUntil(clock.VirtualTime(2 * time.Second)); err != nil {
		log.Fatalf("scenario failed: %v", err)
	}

	healthFinal := make(map[string]health.State)
	for _, t := range allTargets {
		th, _ := registry.GetHealth(t)
		healthFinal[t] = th.State
	}

	return e, ScenarioResult{
		CacheStats: c.Snapshot(), HealthFinalStates: healthFinal, CompletedByTarget: completedByTarget,
		RejectedCount: rejectedCount, CacheHits: cacheHits, CacheMisses: cacheMisses,
	}
}

// healthTransitions scans a trace for health_probe entries where the
// recorded state differs from that target's previous probe -- the same
// transition-summary view 005-D printed, derived here from the richer
// per-tick event stream instead of being recorded as its own event type.
func healthTransitions(trace []vtime.TraceEvent) []string {
	last := make(map[string]string)
	var out []string
	for _, ev := range trace {
		if ev.Type != "health_probe" {
			continue
		}
		state, _ := ev.Fields["state"].(string)
		if last[ev.Entity] != state {
			out = append(out, fmt.Sprintf("t=%.0fms %s -> %s", float64(ev.Time)/1e6, ev.Entity, state))
			last[ev.Entity] = state
		}
	}
	return out
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-G: Full Deterministic Edge Scenario")
	fmt.Println(" Composing cache + routing + load/latency state + failure/recovery -- all individually")
	fmt.Println(" proven in 005-C/D/E -- on one shared virtual timeline.")
	fmt.Println("==========================================================================================")

	e0, res0 := runScenario()
	trace0 := e0.Trace().Events()

	fmt.Println("\nHealth transitions (run 0):")
	for _, s := range healthTransitions(trace0) {
		fmt.Printf("  %s\n", s)
	}
	fmt.Printf("\nCache: %+v\n", res0.CacheStats)
	fmt.Printf("Completed by target: %+v\n", res0.CompletedByTarget)
	fmt.Printf("Rejected requests: %d\n", res0.RejectedCount)
	fmt.Printf("Final health states: %+v\n", res0.HealthFinalStates)
	fmt.Printf("Trace length: %d events\n", len(trace0))

	const runs = 20
	allIdentical := true
	for i := 1; i < runs; i++ {
		e, res := runScenario()
		trace := e.Trace().Events()
		switch {
		case !reflect.DeepEqual(trace, trace0):
			allIdentical = false
			log.Printf("run %d: trace diverged from run 0", i)
		case !reflect.DeepEqual(res, res0):
			allIdentical = false
			log.Printf("run %d: summary result diverged from run 0", i)
		}
		if !allIdentical {
			break
		}
	}

	var finding string
	if allIdentical {
		finding = fmt.Sprintf(
			"All %d runs of the combined scenario (300 requests, 3 targets, a mid-run failure and recovery on the "+
				"fast target edge-b-fast) produced byte-for-byte identical %d-event traces and identical summary "+
				"results: cache %+v, completed-by-target %+v, %d rejected, final health %+v. This experiment exercises "+
				"cache behavior, routing, load/latency state, health detection lag, and failure/recovery together -- "+
				"it does NOT exercise request coalescing, which was never wired into the virtual engine and is not "+
				"claimed here.",
			runs, len(trace0), res0.CacheStats, res0.CompletedByTarget, res0.RejectedCount, res0.HealthFinalStates,
		)
	} else {
		finding = "DETERMINISM VIOLATED: see log output above for the diverging run."
	}
	fmt.Printf("\n%s\n", finding)

	summary := struct {
		Experiment   string         `json:"experiment"`
		Timestamp    string         `json:"timestamp"`
		Runs         int            `json:"runs"`
		TraceLength  int            `json:"trace_length"`
		AllIdentical bool           `json:"all_identical"`
		Result       ScenarioResult `json:"result"`
		Findings     string         `json:"findings"`
	}{
		Experiment: "005-G-full-deterministic-edge-scenario", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Runs: runs, TraceLength: len(trace0), AllIdentical: allIdentical, Result: res0, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005G-full-scenario.json")
	b, _ := json.MarshalIndent(summary, "", "  ")
	os.WriteFile(fname, b, 0644)

	traceFname := filepath.Join(outDirName, "005G-trace.jsonl")
	if err := e0.Trace().WriteJSONLFile(traceFname); err != nil {
		log.Fatalf("failed to write trace: %v", err)
	}

	if !allIdentical {
		log.Fatal("experiment 005-G failed: determinism was violated")
	}

	fmt.Println("\nExperiment 005-G complete.")
}
