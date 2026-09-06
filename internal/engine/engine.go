// Package engine gives the virtual and real execution paths a single
// named front door -- the PRD/TRD's ExperimentEngine interface (§6.1/
// §7, TRD §3), missing per the Stage 8 audit's F-04. Built last in
// Stage 10's plan because it composes pieces every earlier item in
// this stage produced (internal/traffic for real load generation,
// internal/chaos for real fault injection, internal/telemetry for real
// measurement) rather than inventing new execution machinery: Run/
// Replay on VirtualEngine are named wrappers around the already-correct
// replay.RunWorld; Run/Replay on RealEngine orchestrate the same real
// primitives (topology.OriginServer/EdgeServer, proxy.ReverseProxy)
// every cmd/experiment-* binary since Stage 2 has already hand-wired,
// just behind the identical 3-method interface the virtual engine uses.
package engine

import (
	"fmt"
	"time"

	"flashflow/internal/chaos"
	"flashflow/internal/replay"
	"flashflow/internal/telemetry"
	"flashflow/internal/traffic"
)

// RealExperimentConfig is the subset of real-engine configuration an
// Experiment needs beyond what Scenario already carries -- deliberately
// minimal (per-edge processing delay, a traffic pattern, an optional
// chaos schedule), not a full re-exposure of every EdgeConfig/
// proxy.Config knob. A caller needing finer real-engine control (per-
// edge caching, network impairment) should use topology/proxy directly,
// the same way every existing cmd/experiment-* binary already does --
// this type exists to make the COMMON case (a handful of edges, one
// traffic pattern, an optional fault schedule) a one-call experiment,
// not to replace direct access to the underlying packages.
type RealExperimentConfig struct {
	OriginDelay    time.Duration
	Edges          map[string]time.Duration // instance name -> DefaultDelay; also feeds the routing policy's TargetProfile.ServiceTime, the real-engine analog of a virtual target's fixed service time
	TrafficPattern traffic.Pattern
	TrafficParams  traffic.Params // Horizon governs how long the real run's dispatch window (and the whole Run/Replay call) lasts
	Chaos          chaos.Schedule
}

// Experiment bundles everything one experiment needs to run on either
// engine: exogenous conditions (Scenario, reused as-is for the virtual
// engine and as the source of Seeds for the real engine's own traffic/
// policy randomness), the policy under test, and -- only when a real
// run is wanted -- Real's engine-specific configuration.
type Experiment struct {
	ID       string
	Name     string
	Scenario replay.Scenario
	Policy   replay.PolicySpec
	Real     *RealExperimentConfig // nil means this Experiment is virtual-only; RealEngine.Prepare/Run/Replay reject a nil Real
}

// RealMetrics is what RealEngine.Run/Replay report instead of a
// replay.WorldResult -- there is no virtual-shaped WorldResult for a
// real run to produce (no synthetic Records/Trace; the real engine's
// own telemetry.Metrics and completed-request count are the analogous
// evidence).
type RealMetrics struct {
	Metrics  telemetry.Metrics
	Requests int // successfully completed requests during the run
}

// RunResult is what either engine's Run/Replay returns. Exactly one of
// WorldResult/Real is set, never both and never neither -- a disclosed
// asymmetry, not an oversight: the two engines produce fundamentally
// different evidence shapes (a full causal Trace vs. point-in-time
// telemetry counters), and forcing them into one shared result shape
// would either lose the virtual engine's exact trace or fabricate one
// for the real engine that doesn't exist.
type RunResult struct {
	Engine      string // "virtual" or "real"
	WorldResult *replay.WorldResult
	Real        *RealMetrics
}

// ValidateConsistency checks that exp.Scenario and exp.Real describe
// the SAME experiment, not just two configurations that happen to
// share an ID and Policy -- a real, adversarially-confirmed gap the
// Stage 10 demo-readiness audit found: before this check existed, an
// Experiment could carry a Scenario describing one topology (say,
// three targets at 5/50/500ms) and a Real config describing a
// completely different one (a single 1ms edge), and neither engine's
// own Prepare noticed or complained.
//
// The check: the set of target names in Scenario.Targets must be
// EXACTLY the set of edge names in Real.Edges (same names, same count,
// checked both directions). This does not, and cannot without far more
// invasive bookkeeping, guarantee identical BEHAVIOR between the two
// engines' runs of "the same" experiment -- a real edge's actual
// latency depends on real scheduling and network conditions the
// virtual engine's fixed ServiceTime never does. What it does guarantee
// is that the two configurations describe the same named topology,
// which is the minimum bar for calling them "the same experiment" at
// all, and it turns a silent, undetected mismatch into an immediate,
// specific error.
//
// Only meaningful when exp.Real is non-nil; a purely virtual Experiment
// has nothing to cross-check against, so this returns nil for one.
func ValidateConsistency(exp Experiment) error {
	if exp.Real == nil {
		return nil
	}
	scenarioNames := make(map[string]bool, len(exp.Scenario.Targets))
	for _, t := range exp.Scenario.Targets {
		scenarioNames[t.Name] = true
	}
	realNames := make(map[string]bool, len(exp.Real.Edges))
	for name := range exp.Real.Edges {
		realNames[name] = true
	}
	if len(scenarioNames) != len(realNames) {
		return fmt.Errorf("engine: experiment %q has %d Scenario target(s) but %d Real edge(s) -- Scenario and Real must describe the same topology",
			exp.ID, len(scenarioNames), len(realNames))
	}
	for name := range scenarioNames {
		if !realNames[name] {
			return fmt.Errorf("engine: experiment %q's Scenario target %q has no matching Real edge of the same name", exp.ID, name)
		}
	}
	for name := range realNames {
		if !scenarioNames[name] {
			return fmt.Errorf("engine: experiment %q's Real edge %q has no matching Scenario target of the same name", exp.ID, name)
		}
	}
	return nil
}

// ExperimentEngine is the shared contract both execution paths
// implement. Prepare validates exp is well-formed for this engine
// (rejecting an unusable Experiment before any real resource is
// started, or before a virtual run wastes a cycle on a Scenario that
// can't execute); Run executes exp.Policy; Replay executes an
// alternate policy against the identical exogenous conditions -- the
// counterfactual-replay operation both engines' own Stage 5/7
// machinery already performs, now reachable through one interface
// regardless of which engine is backing it.
type ExperimentEngine interface {
	Prepare(exp Experiment) error
	Run(exp Experiment) (RunResult, error)
	Replay(exp Experiment, policy replay.PolicySpec) (RunResult, error)
}

// Compile-time proof that both engines actually satisfy the shared
// interface -- the entire point of this package, verified by the
// compiler rather than left as an assertion in prose.
var (
	_ ExperimentEngine = VirtualEngine{}
	_ ExperimentEngine = RealEngine{}
)
