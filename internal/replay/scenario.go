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
	"crypto/sha256"
	"encoding/binary"
	"fmt"
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
	// Seeds replaces a single flat Seed int64 field (Stage 10, §10.3):
	// this project's own generators (internal/tuning/scenario.go) drew
	// target count/service-times, arrival jitter, and failure timing all
	// from ONE shared *rand.Rand, meaning any change to how many draws
	// one axis consumed silently shifted every axis generated after it
	// -- an accidental coupling between conceptually independent
	// exogenous dimensions, not a deliberate design. SeedTree gives each
	// axis its own seed so a caller can hold Traffic fixed while sweeping
	// Failure, for instance, and get exactly that: identical arrivals,
	// varying failure windows, not an entangled mix of both changing at
	// once.
	Seeds SeedTree
}

// SeedTree is a Scenario's full seed identity, split into independent
// axes rather than one flat root -- the "widen the type, don't just
// derive-and-forget" design decision Stage 10 locked in (see
// docs/StageArtifacts/Stage10-Plan.md's confirmed design decisions):
// genuine independent-axis control was judged more valuable than
// keeping Scenario down to a single int64, since a derive-only helper
// that immediately discarded the sub-seeds would make "hold Traffic
// fixed, vary Failure" impossible to express at all.
type SeedTree struct {
	Global   int64 // the root a SeedTree was derived from, if it was derived (DeriveSeeds) -- kept for provenance/logging, not consumed by any policy or generator directly
	Traffic  int64 // arrival timing/jitter
	Topology int64 // target count, names, service-time draws
	Failure  int64 // failure-window presence, timing, and target selection
	Policy   int64 // a policy's own randomness (e.g. P2C's pair sampling) -- was PolicySpec.New's flat seed parameter
}

// DeriveSeeds is the compatibility path for every call site that only
// has (or only needs) one root seed: it produces a SeedTree whose four
// sub-seeds are independent-LOOKING derivations of global (via
// SHA-256("<label>:<global>"), truncated to a non-negative int64), so a
// single literal `Seeds: replay.DeriveSeeds(42)` is a purely mechanical,
// behavior-preserving replacement for the old `Seed: 42` at every
// existing call site that never needed independent-axis control in the
// first place.
func DeriveSeeds(global int64) SeedTree {
	derive := func(label string) int64 {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", label, global)))
		v := int64(binary.BigEndian.Uint64(sum[:8]))
		if v < 0 {
			v = -v // keep non-negative: callers doing e.g. rng.Int63n(n) on a sub-seed as a source for further derivation should never see a surprising negative root
		}
		return v
	}
	return SeedTree{
		Global:   global,
		Traffic:  derive("traffic"),
		Topology: derive("topology"),
		Failure:  derive("failure"),
		Policy:   derive("policy"),
	}
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
