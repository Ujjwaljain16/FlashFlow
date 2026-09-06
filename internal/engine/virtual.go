package engine

import (
	"fmt"

	"flashflow/internal/replay"
)

// VirtualEngine implements ExperimentEngine over replay.RunWorld -- a
// named front door onto machinery that was already correct (Stage 7's
// counterfactual replay engine), not a new execution path.
type VirtualEngine struct{}

// NewVirtualEngine constructs a VirtualEngine. Stateless: every call
// to Run/Replay builds its own fresh World via replay.RunWorld,
// matching that function's own "nothing shared between calls"
// guarantee.
func NewVirtualEngine() VirtualEngine {
	return VirtualEngine{}
}

// Prepare rejects an Experiment the virtual engine cannot execute --
// today, that means having at least one target and one arrival;
// replay.RunWorld itself would otherwise just run a degenerate,
// pointless World rather than fail loudly.
func (VirtualEngine) Prepare(exp Experiment) error {
	if len(exp.Scenario.Targets) == 0 {
		return fmt.Errorf("engine: experiment %q has no targets", exp.ID)
	}
	if len(exp.Scenario.Arrivals) == 0 {
		return fmt.Errorf("engine: experiment %q has no arrivals", exp.ID)
	}
	if exp.Policy.New == nil {
		return fmt.Errorf("engine: experiment %q has no policy constructor", exp.ID)
	}
	return nil
}

// Run executes exp.Policy against exp.Scenario.
func (v VirtualEngine) Run(exp Experiment) (RunResult, error) {
	if err := v.Prepare(exp); err != nil {
		return RunResult{}, err
	}
	result, err := replay.RunWorld(exp.Scenario, exp.Policy)
	if err != nil {
		return RunResult{}, fmt.Errorf("engine: virtual run of %q: %w", exp.ID, err)
	}
	return RunResult{Engine: "virtual", WorldResult: &result}, nil
}

// Replay executes policy against the identical exp.Scenario -- the
// counterfactual-replay operation: same exogenous conditions, a
// different endogenous decision-maker.
//
// A note on Stage 9's SameProtocol check (F-18): SameProtocol exists to
// catch two DIFFERENT Scenarios that were meant to be a counterfactual
// pair but accidentally differ in a protocol field (UseHealthRegistry/
// ProbeInterval/Horizon), not just their physics. This method's own
// signature only ever involves ONE Scenario (exp.Scenario) run under
// two policies, so exp.Scenario.SameProtocol(exp.Scenario) would be
// unconditionally true by construction -- adding that call here would
// be decorative, not a real check, and this project does not add
// verification theater. The real place SameProtocol earns its keep is
// when a CALLER compares RunResults from two separate Run/Replay calls
// against two independently-built Experiments (e.g. before treating
// their Traces as comparable via replay.FirstDivergence) -- see
// CompareProtocol below, which that caller should use instead.
func (v VirtualEngine) Replay(exp Experiment, policy replay.PolicySpec) (RunResult, error) {
	if err := v.Prepare(exp); err != nil {
		return RunResult{}, err
	}
	if policy.New == nil {
		return RunResult{}, fmt.Errorf("engine: replay policy for %q has no constructor", exp.ID)
	}
	result, err := replay.RunWorld(exp.Scenario, policy)
	if err != nil {
		return RunResult{}, fmt.Errorf("engine: virtual replay of %q: %w", exp.ID, err)
	}
	return RunResult{Engine: "virtual", WorldResult: &result}, nil
}

// CompareProtocol is the real enforcement point for Stage 9's
// SameProtocol check in an ExperimentEngine-based workflow: call this
// before comparing two Experiments' RunResults (e.g. via
// replay.FirstDivergence on their WorldResults' Traces) to confirm they
// differ only in physics, not in the run-length/health-registry
// protocol fields that would otherwise make an apparent "divergence" a
// meaningless run-length artifact instead of a real policy effect.
func CompareProtocol(a, b Experiment) error {
	if !a.Scenario.SameProtocol(b.Scenario) {
		return fmt.Errorf("engine: experiments %q and %q have incompatible protocol fields (UseHealthRegistry/ProbeInterval/Horizon) -- their WorldResults are not a valid counterfactual comparison", a.ID, b.ID)
	}
	return nil
}
