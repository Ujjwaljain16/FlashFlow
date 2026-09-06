// Package dashboard is the backend logic behind cmd/dashboard: the
// operator interface master context rules 29-35 ask for, deliberately
// built as a thin layer over machinery this project already has.
// Running or comparing a policy in the Playground calls the exact same
// internal/replay.RunWorld and FirstDivergence every experiment since
// Stage 7 has used -- the dashboard does not reimplement or shortcut
// the engine, it exposes it. Per rule 34, this package reads experiment
// artifacts and drives the real engine; it never becomes a second
// source of truth.
package dashboard

import (
	"fmt"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
)

// PlaygroundScenario is the one canonical, fixed scenario the
// dashboard's Playground runs -- 3 edges (A fast/stable, B starts fast
// then fails and recovers, C medium), mixed hot/cold keys exercising
// AdaptiveSelector's Cache signal, health detection, adaptive routing.
// This is deliberately close to, but not identical to, the "B:
// initially best -> becomes slow -> fails -> recovers" three-phase
// story sometimes described for a demo scenario: internal/replay.Scenario
// has no time-varying per-target service time (the same documented
// limitation 008-A's scenario generator and 008-F's challenge scenarios
// already state), so the "becomes slow" middle phase isn't
// expressible here without extending the engine specifically for a
// demo -- not earned. What IS expressible, and is exactly what's
// built: B is genuinely the fastest target, fails for real (health
// detection, not just a slowdown), and recovers to its original speed
// -- a real, honestly-scoped two-phase story instead of an
// unimplementable three-phase one.
func PlaygroundScenario() replay.Scenario {
	const requests = 300
	const spacing = 10 * time.Millisecond
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: playgroundKeyFor(i)}
	}
	return replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 20 * time.Millisecond}, // fast, stable
			{Name: "edge-b", ServiceTime: 15 * time.Millisecond}, // fastest -- until it fails
			{Name: "edge-c", ServiceTime: 60 * time.Millisecond}, // medium
		},
		Arrivals: arrivals,
		Failures: []replay.FailureWindow{
			{Target: "edge-b", DownAt: clock.VirtualTime(1000 * time.Millisecond), UpAt: clock.VirtualTime(2000 * time.Millisecond)},
		},
		Horizon: clock.VirtualTime(3500 * time.Millisecond),
		Seeds:   replay.DeriveSeeds(1),
	}
}

func playgroundKeyFor(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	switch i % 6 {
	case 1:
		return "/cold-1"
	case 3:
		return "/cold-2"
	default:
		return "/cold-3"
	}
}

// PolicyNames lists the Playground's selectable policies, in the order
// the UI presents them.
func PolicyNames() []string {
	return []string{"round-robin", "weighted-round-robin", "least-connections", "ewma", "p2c-load", "adaptive"}
}

// PolicyByName resolves a Playground policy name to its PolicySpec.
// Rejects anything not on PolicyNames' own list -- this is reachable
// from an HTTP handler, so the input is untrusted.
func PolicyByName(name string) (replay.PolicySpec, error) {
	switch name {
	case "round-robin":
		return replay.RoundRobinPolicy(), nil
	case "weighted-round-robin":
		return replay.WeightedRoundRobinPolicy(), nil
	case "least-connections":
		return replay.LeastConnectionsPolicy(), nil
	case "ewma":
		return replay.EWMAPolicy(), nil
	case "p2c-load":
		return replay.P2CLoadPolicy(), nil
	case "adaptive":
		return replay.AdaptivePolicy(), nil
	default:
		return replay.PolicySpec{}, fmt.Errorf("dashboard: unknown policy %q", name)
	}
}

// RunSummary is what the Playground UI needs after one run: the full
// trace (for the event timeline and topology animation), per-target
// completion counts (for the topology view's sizing), and a small set
// of derived metrics.
type RunSummary struct {
	Policy            string                   `json:"policy"`
	Records           []replay.SelectionRecord `json:"records"`
	Trace             []TraceEventView         `json:"trace"`
	CompletedByTarget map[string]int           `json:"completed_by_target"`
	RejectedCount     int                      `json:"rejected_count"`
	TotalRequests     int                      `json:"total_requests"`
	MeanLatencyMs     float64                  `json:"mean_latency_ms"`
	P99LatencyMs      float64                  `json:"p99_latency_ms"`
}

// TraceEventView mirrors vtime.TraceEvent with JSON field names the
// frontend can consume directly, and a millisecond-scaled time (the
// frontend works entirely in ms, never raw nanoseconds).
type TraceEventView struct {
	TimeMs float64        `json:"time_ms"`
	Type   string         `json:"type"`
	Entity string         `json:"entity,omitempty"`
	Fields map[string]any `json:"fields,omitempty"`
}

// RunPlayground runs policyName against PlaygroundScenario and
// summarizes the result for the UI.
func RunPlayground(policyName string) (RunSummary, error) {
	spec, err := PolicyByName(policyName)
	if err != nil {
		return RunSummary{}, err
	}
	result, err := replay.RunWorld(PlaygroundScenario(), spec)
	if err != nil {
		return RunSummary{}, fmt.Errorf("dashboard: running playground: %w", err)
	}
	return summarize(policyName, result), nil
}

func summarize(policyName string, result replay.WorldResult) RunSummary {
	trace := make([]TraceEventView, len(result.Trace))
	for i, ev := range result.Trace {
		trace[i] = TraceEventView{TimeMs: float64(ev.Time) / 1e6, Type: ev.Type, Entity: ev.Entity, Fields: ev.Fields}
	}

	total := len(result.Records) + result.RejectedCount
	summary := RunSummary{
		Policy: policyName, Records: result.Records, Trace: trace,
		CompletedByTarget: result.CompletedByTarget, RejectedCount: result.RejectedCount,
		TotalRequests: total,
	}

	if len(result.Completions) > 0 {
		latenciesMs := make([]float64, len(result.Completions))
		for i, c := range result.Completions {
			latenciesMs[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		summary.MeanLatencyMs, _ = statistics.Mean(latenciesMs)
		summary.P99LatencyMs, _ = statistics.Percentile(latenciesMs, 99)
	}
	return summary
}

// CompareSummary is the Playground's counterfactual view: two runs
// against the byte-for-byte identical Scenario, plus where (and
// whether) their traces first diverge -- the "first point of
// divergence" master context rule 31 specifically asks the dashboard
// to surface, computed with the same replay.FirstDivergence every
// counterfactual experiment since 007-F has used, not a
// dashboard-specific reimplementation.
type CompareSummary struct {
	Baseline         RunSummary `json:"baseline"`
	Counterfactual   RunSummary `json:"counterfactual"`
	Diverged         bool       `json:"diverged"`
	DivergenceIndex  int        `json:"divergence_index"`
	DivergenceTimeMs float64    `json:"divergence_time_ms"`
}

// ComparePlayground runs baselinePolicy and counterfactualPolicy
// against the identical PlaygroundScenario and reports their first
// point of divergence, if any.
func ComparePlayground(baselinePolicy, counterfactualPolicy string) (CompareSummary, error) {
	baselineSpec, err := PolicyByName(baselinePolicy)
	if err != nil {
		return CompareSummary{}, err
	}
	cfSpec, err := PolicyByName(counterfactualPolicy)
	if err != nil {
		return CompareSummary{}, err
	}

	scenario := PlaygroundScenario()
	baselineResult, err := replay.RunWorld(scenario, baselineSpec)
	if err != nil {
		return CompareSummary{}, fmt.Errorf("dashboard: running baseline: %w", err)
	}
	cfResult, err := replay.RunWorld(scenario, cfSpec)
	if err != nil {
		return CompareSummary{}, fmt.Errorf("dashboard: running counterfactual: %w", err)
	}

	idx, diverged := replay.FirstDivergence(baselineResult.Trace, cfResult.Trace)
	summary := CompareSummary{
		Baseline: summarize(baselinePolicy, baselineResult), Counterfactual: summarize(counterfactualPolicy, cfResult),
		Diverged: diverged, DivergenceIndex: idx,
	}
	if diverged && idx < len(baselineResult.Trace) {
		summary.DivergenceTimeMs = float64(baselineResult.Trace[idx].Time) / 1e6
	}
	return summary, nil
}
