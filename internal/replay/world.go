package replay

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

// Trace event types this package records via vtime.Engine.Record. Typed
// here (rather than left as inline string literals at each call site) so
// the vocabulary RunWorld actually emits is discoverable in one place —
// it was previously four untyped literals scattered across this
// function, each freely restatable without a compiler-checked link
// between them.
const (
	eventHealthProbe      = "health_probe"
	eventRequestRejected  = "request_rejected"
	eventRequestRouted    = "request_routed"
	eventRequestCompleted = "request_completed"
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
// across calls. seeds is threaded through from the Scenario (Stage 10,
// §10.3: widened from a single flat seed to a SeedTree) so that any
// policy needing randomness (P2C, via seeds.Policy) is reproducible from
// the Scenario alone, the same as every other exogenous input, while
// still letting a caller vary a policy's own randomness independently of
// traffic/topology/failure seeds if it ever needs to. targets is the
// Scenario's own target list, threaded through for the one policy that
// needs to know it ahead of time (WeightedRoundRobinPolicy's static
// capacity weights) -- every other policy ignores the parameter
// entirely, the same as they already ignore clk or seeds when they don't
// need them.
type PolicySpec struct {
	Name string
	New  func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation)
}

// SelectionRecord is one endogenous decision a World made in response to
// one exogenous Arrival.
type SelectionRecord struct {
	VirtualTimeMs float64 `json:"virtual_time_ms"`
	Key           string  `json:"key"`
	Target        string  `json:"target"`
}

// CompletionRecord is one request's realized latency -- separate from
// SelectionRecord because the latency isn't known until the completion
// callback fires, strictly after the selection that caused it. No Stage
// 7 experiment needed raw per-request latency (they worked from
// LatencyTracker's smoothed estimate or from selection distributions
// alone); Stage 8's tuning objective does, since a percentile like p99
// can't be reconstructed from an EWMA estimate or from aggregate counts.
type CompletionRecord struct {
	VirtualTimeMs float64       `json:"virtual_time_ms"`
	Target        string        `json:"target"`
	Latency       time.Duration `json:"latency_ns"`
}

// WorldResult is everything one completed World run produced: its full
// causal Trace (for exact identity/divergence comparisons between runs)
// plus a convenience summary for comparing policies against each other.
type WorldResult struct {
	Records           []SelectionRecord
	Completions       []CompletionRecord
	Trace             []vtime.TraceEvent
	CompletedByTarget map[string]int
	RejectedCount     int
	// InFlightAtHorizon is len(Records)-len(Completions): requests that
	// were successfully routed but whose scheduled completion had not
	// yet fired when the run stopped (only possible when Scenario.Horizon
	// truncates a run before every in-flight request's service time
	// elapses — RunUntilEmpty, by definition, never leaves this nonzero).
	// Surfaced explicitly rather than silently — len(Records) is not
	// guaranteed to equal sum(CompletedByTarget)+RejectedCount whenever
	// this is nonzero, and a consumer computing a completion rate without
	// checking it would silently undercount in-flight work as neither
	// success nor rejection.
	InFlightAtHorizon int
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
	selector, instr := spec.New(e.Clock(), scenario.Seeds, scenario.Targets)

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
				e.Record(eventHealthProbe, t, map[string]any{"success": up[t], "state": string(state)})
			}
		}); err != nil {
			return WorldResult{}, fmt.Errorf("replay: starting probe ticker: %w", err)
		}
	}

	serviceTimes := scenario.serviceTimes()
	var records []SelectionRecord
	var completions []CompletionRecord
	completedByTarget := make(map[string]int)
	rejectedCount := 0
	// Every independent scheduling failure is appended, not overwritten —
	// a single shared error variable previously meant that if multiple
	// arrivals each hit a scheduling error, only the last one's message
	// survived, with no visibility into how many were actually affected.
	var scheduleErrs []error

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
				e.Record(eventRequestRejected, arrival.Key, map[string]any{"reason": "no_healthy_targets"})
				return
			}

			r := httptest.NewRequest(http.MethodGet, arrival.Key, nil)
			target, err := selector.SelectTarget(r, available)
			if err != nil {
				scheduleErrs = append(scheduleErrs, fmt.Errorf("replay: selection failed for key %q at %v: %w", arrival.Key, e.Now(), err))
				return
			}
			now := e.Now()
			records = append(records, SelectionRecord{VirtualTimeMs: msF(now), Key: arrival.Key, Target: target})
			e.Record(eventRequestRouted, arrival.Key, map[string]any{"target": target})
			instr.OnDispatch(target)

			svc := serviceTimes[target]
			if _, err := e.Schedule(now.Add(svc), func() {
				latency := e.Now().Sub(now)
				instr.OnComplete(target, latency)
				completedByTarget[target]++
				completions = append(completions, CompletionRecord{VirtualTimeMs: msF(e.Now()), Target: target, Latency: latency})
				e.Record(eventRequestCompleted, arrival.Key, map[string]any{"target": target})
			}); err != nil {
				scheduleErrs = append(scheduleErrs, fmt.Errorf("replay: scheduling completion for key %q, target %q: %w", arrival.Key, target, err))
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
	result := WorldResult{
		Records:           records,
		Completions:       completions,
		Trace:             e.Trace().Events(),
		CompletedByTarget: completedByTarget,
		RejectedCount:     rejectedCount,
		InFlightAtHorizon: len(records) - len(completions),
	}
	if len(scheduleErrs) > 0 {
		// Partial results are returned alongside the joined error (rather
		// than a bare zero-value WorldResult) so a caller can see exactly
		// how much of the run succeeded before the failure(s), instead of
		// losing every arrival that scheduled cleanly because a handful
		// of others didn't.
		return result, errors.Join(scheduleErrs...)
	}
	return result, nil
}

func msF(t clock.VirtualTime) float64 { return float64(t) / 1e6 }
