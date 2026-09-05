// Package replay implements FlashFlow's counterfactual replay engine:
// running the same external conditions against different routing
// policies (or the same policy at different points in its own history)
// to ask "what would have happened instead?"
//
// The whole engine rests on one discipline, stated in the Stage 7 master
// context and enforced by this package's types rather than left as a
// convention: exogenous state (Scenario -- what the world does to a
// policy) is completely separate from endogenous state (everything a
// PolicySpec's routing selector and trackers build up in response). A
// Scenario is a plain, immutable value; RunWorld constructs a fresh
// selector, fresh trackers, and a fresh *vtime.Engine on every call.
// Two RunWorld calls given the same Scenario and PolicySpec never share
// a single mutable object -- that is what makes them a genuine
// counterfactual pair instead of two runs of one shared simulation.
package replay

import (
	"time"

	"flashflow/internal/clock"
)

// TargetProfile is exogenous "physics": how long a target takes to serve
// a request. This is a property of the world, fixed regardless of which
// policy routes to it -- not a routing decision.
type TargetProfile struct {
	Name        string
	ServiceTime time.Duration
}

// Arrival is one exogenous request arrival: when it happens and which
// cache key it carries. It never says which target serves it -- that is
// the one thing every World decides for itself.
type Arrival struct {
	At  clock.VirtualTime
	Key string
}

// FailureWindow is an exogenous ground-truth outage: Target is actually
// down from DownAt until UpAt, independent of when (or whether) any
// World's own health.Registry detects it -- the same real/observed
// asymmetry 005-D and 005-G established.
type FailureWindow struct {
	Target string
	DownAt clock.VirtualTime
	UpAt   clock.VirtualTime
}

// Scenario is everything exogenous about one experiment: the world's
// physics (Targets), its external event schedule (Arrivals, Failures),
// and its one source of external randomness (Seed, for any policy that
// needs one). Nothing in a Scenario depends on which policy will be
// evaluated against it -- that independence is what makes comparing two
// policies against the identical Scenario a fair counterfactual, not two
// different experiments that happen to look similar.
type Scenario struct {
	Targets  []TargetProfile
	Arrivals []Arrival
	Failures []FailureWindow
	// UseHealthRegistry forces RunWorld to build a health.Registry and
	// probe Ticker even when Failures is empty (every target always
	// reports up). This matters for counterfactual comparisons: a
	// Scenario with no failures at all and one with a failure introduced
	// after some cutoff should both run identical health-probe machinery
	// up to that cutoff, so the only difference in their traces is the
	// failure's actual effect -- not the mere presence or absence of
	// probe-related trace events. RunWorld builds the registry whenever
	// this is true or Failures is non-empty.
	UseHealthRegistry bool
	ProbeInterval     time.Duration // 0 means "not set"; RunWorld defaults it when a registry is built
	Horizon           clock.VirtualTime
	Seed              int64
}

// TargetNames returns the scenario's target names in Targets order.
func (s Scenario) TargetNames() []string {
	names := make([]string, len(s.Targets))
	for i, t := range s.Targets {
		names[i] = t.Name
	}
	return names
}

func (s Scenario) serviceTimes() map[string]time.Duration {
	m := make(map[string]time.Duration, len(s.Targets))
	for _, t := range s.Targets {
		m[t.Name] = t.ServiceTime
	}
	return m
}

// SameProtocol reports whether s and other agree on the experiment-
// protocol fields RunWorld's own execution mechanics depend on
// (UseHealthRegistry, ProbeInterval, Horizon) — as distinct from the
// world's actual physics (Targets/Arrivals/Failures/Seed). These fields
// are exogenous in the sense that both are inputs fixed before a run
// starts, but they are protocol knobs, not world physics: nothing in the
// type system stops constructing two Scenarios for a counterfactual
// comparison that differ in, say, Horizon alone, which FirstDivergence
// would then report as a policy-caused divergence when it is really just
// a run-length artifact. Every current caller gets this right by hand
// (copy the baseline Scenario, mutate only a physics field); SameProtocol
// exists so that discipline can be checked instead of merely trusted —
// see ComparePolicies.
func (s Scenario) SameProtocol(other Scenario) bool {
	return s.UseHealthRegistry == other.UseHealthRegistry &&
		s.ProbeInterval == other.ProbeInterval &&
		s.Horizon == other.Horizon
}
