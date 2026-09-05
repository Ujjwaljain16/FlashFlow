package replay

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

// Instrumentation feeds a World's dispatch/completion events into
// whatever endogenous trackers a policy needs (LoadTracker,
// LatencyTracker, both, or neither), without World needing to know which
// policy it is running. See policies.go for the concrete adapters.
type Instrumentation interface {
	OnDispatch(target string)
	OnComplete(target string, latency time.Duration)
}

// NoInstrumentation is for policies that maintain no endogenous state at
// all (Round Robin).
type NoInstrumentation struct{}

func (NoInstrumentation) OnDispatch(string)                {}
func (NoInstrumentation) OnComplete(string, time.Duration) {}

// PolicySpec describes one routing policy under test. New is called
// exactly once per RunWorld call, and must construct a completely fresh
// selector and fresh trackers every time -- never reuse or share one
// across calls. seed is threaded through from the Scenario so that any
// policy needing randomness (P2C) is reproducible from the Scenario
// alone, the same as every other exogenous input.
type PolicySpec struct {
	Name string
	New  func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation)
}

// SelectionRecord is one endogenous decision a World made in response to
// one exogenous Arrival.
type SelectionRecord struct {
	VirtualTimeMs float64 `json:"virtual_time_ms"`
	Key           string  `json:"key"`
	Target        string  `json:"target"`
}

// WorldResult is everything one completed World run produced: its full
// causal Trace (for exact identity/divergence comparisons between runs)
// plus a convenience summary for comparing policies against each other.
type WorldResult struct {
	Records           []SelectionRecord
	Trace             []vtime.TraceEvent
	CompletedByTarget map[string]int
	RejectedCount     int
}

// RunWorld constructs one completely fresh World from scenario and spec
// and runs it to completion.
//
// "Fresh" is the entire point: this call builds its own *vtime.Engine,
// its own health.Registry (only if scenario.Failures is non-empty), and
// -- via spec.New -- its own selector and trackers. Nothing referenced
// by a previous RunWorld call is read or mutated by this one. Two
// RunWorld calls given the identical Scenario and PolicySpec therefore
// form a true counterfactual pair: same exogenous inputs, independently-
// evolving endogenous state. world_test.go verifies this property
// directly (identity, divergence, and isolation tests) rather than
// leaving it as an unchecked design intent.
func RunWorld(scenario Scenario, spec PolicySpec) (WorldResult, error) {
	e := vtime.NewEngine(0)
	selector, instr := spec.New(e.Clock(), scenario.Seed)

	allTargets := scenario.TargetNames()
	var registry *health.Registry
	if scenario.UseHealthRegistry || len(scenario.Failures) > 0 {
		registry = health.NewRegistry(e.Clock(), health.DefaultConfig())
		for _, t := range allTargets {
			registry.RegisterTarget(t)
		}

		up := make(map[string]bool, len(allTargets))
		for _, t := range allTargets {
			up[t] = true
		}
		for _, f := range scenario.Failures {
			f := f
			if _, err := e.Schedule(f.DownAt, func() { up[f.Target] = false }); err != nil {
				return WorldResult{}, fmt.Errorf("replay: scheduling failure start: %w", err)
			}
			if _, err := e.Schedule(f.UpAt, func() { up[f.Target] = true }); err != nil {
				return WorldResult{}, fmt.Errorf("replay: scheduling failure end: %w", err)
			}
		}

		interval := scenario.ProbeInterval
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		if _, err := e.NewTicker(0, interval, func() {
			for _, t := range allTargets {
				state := registry.RecordProbeResult(t, up[t])
				e.Record("health_probe", t, map[string]any{"success": up[t], "state": string(state)})
			}
		}); err != nil {
			return WorldResult{}, fmt.Errorf("replay: starting probe ticker: %w", err)
		}
	}

	serviceTimes := scenario.serviceTimes()
	var records []SelectionRecord
	completedByTarget := make(map[string]int)
	rejectedCount := 0
	var scheduleErr error

	for _, arrival := range scenario.Arrivals {
		arrival := arrival
		_, err := e.Schedule(arrival.At, func() {
			var available []string
			if registry != nil {
				for _, t := range allTargets {
					if registry.IsAvailable(t) {
						available = append(available, t)
					}
				}
			} else {
				available = allTargets
			}
			if len(available) == 0 {
				rejectedCount++
				e.Record("request_rejected", arrival.Key, map[string]any{"reason": "no_healthy_targets"})
				return
			}

			r := httptest.NewRequest(http.MethodGet, arrival.Key, nil)
			target, err := selector.SelectTarget(r, available)
			if err != nil {
				scheduleErr = fmt.Errorf("replay: selection failed: %w", err)
				return
			}
			now := e.Now()
			records = append(records, SelectionRecord{VirtualTimeMs: msF(now), Key: arrival.Key, Target: target})
			e.Record("request_routed", arrival.Key, map[string]any{"target": target})
			instr.OnDispatch(target)

			svc := serviceTimes[target]
			if _, err := e.Schedule(now.Add(svc), func() {
				instr.OnComplete(target, e.Now().Sub(now))
				completedByTarget[target]++
				e.Record("request_completed", arrival.Key, map[string]any{"target": target})
			}); err != nil {
				scheduleErr = fmt.Errorf("replay: scheduling completion: %w", err)
			}
		})
		if err != nil {
			return WorldResult{}, fmt.Errorf("replay: scheduling arrival: %w", err)
		}
	}

	if scenario.Horizon > 0 {
		if err := e.RunUntil(scenario.Horizon); err != nil {
			return WorldResult{}, fmt.Errorf("replay: run failed: %w", err)
		}
	} else if err := e.RunUntilEmpty(); err != nil {
		return WorldResult{}, fmt.Errorf("replay: run failed: %w", err)
	}
	if scheduleErr != nil {
		return WorldResult{}, scheduleErr
	}

	return WorldResult{
		Records:           records,
		Trace:             e.Trace().Events(),
		CompletedByTarget: completedByTarget,
		RejectedCount:     rejectedCount,
	}, nil
}

func msF(t clock.VirtualTime) float64 { return float64(t) / 1e6 }
