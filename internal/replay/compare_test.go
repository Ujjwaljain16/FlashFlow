package replay

import (
	"errors"
	"testing"
	"time"

	"flashflow/internal/clock"
)

func baseCompareScenario() Scenario {
	return Scenario{
		Targets:  []TargetProfile{{Name: "a", ServiceTime: 10 * time.Millisecond}},
		Arrivals: []Arrival{{At: 0, Key: "/x"}},
		Horizon:  clock.VirtualTime((100 * time.Millisecond).Nanoseconds()),
		Seed:     1,
	}
}

// TestScenario_SameProtocol_TrueForIdenticalProtocolFields confirms two
// Scenarios that differ only in physics (Targets) but share
// UseHealthRegistry/ProbeInterval/Horizon are considered protocol-
// compatible.
func TestScenario_SameProtocol_TrueForIdenticalProtocolFields(t *testing.T) {
	a := baseCompareScenario()
	b := baseCompareScenario()
	b.Targets = []TargetProfile{{Name: "different", ServiceTime: 999 * time.Millisecond}}

	if !a.SameProtocol(b) {
		t.Fatalf("expected SameProtocol=true for Scenarios differing only in physics (Targets)")
	}
}

// TestScenario_SameProtocol_FalseForDifferingHorizon regression-tests
// F-18: nothing previously stopped comparing two Scenarios that differ
// only in Horizon, which FirstDivergence would then misreport as a
// policy-caused divergence rather than a run-length artifact.
func TestScenario_SameProtocol_FalseForDifferingHorizon(t *testing.T) {
	a := baseCompareScenario()
	b := baseCompareScenario()
	b.Horizon = a.Horizon * 2

	if a.SameProtocol(b) {
		t.Fatalf("expected SameProtocol=false for Scenarios differing in Horizon")
	}
}

// TestComparePolicies_RejectsProtocolMismatchBeforeComparingTraces
// regression-tests F-18 end-to-end: ComparePolicies must refuse to run
// FirstDivergence at all when the originating Scenarios disagree on
// protocol fields, rather than silently reporting whatever divergence
// index the mismatched run lengths happen to produce.
func TestComparePolicies_RejectsProtocolMismatchBeforeComparingTraces(t *testing.T) {
	a := baseCompareScenario()
	b := baseCompareScenario()
	b.ProbeInterval = a.ProbeInterval + time.Millisecond

	resultA, err := RunWorld(a, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(a) failed: %v", err)
	}
	resultB, err := RunWorld(b, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(b) failed: %v", err)
	}

	_, _, err = ComparePolicies(a, b, resultA.Trace, resultB.Trace)
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("expected ErrProtocolMismatch, got %v", err)
	}
}

// TestComparePolicies_MatchesFirstDivergenceWhenProtocolAgrees confirms
// ComparePolicies behaves identically to calling FirstDivergence directly
// once the protocol precondition is satisfied -- it adds a guardrail, not
// a behavior change, for the compatible case every existing caller
// already relies on.
func TestComparePolicies_MatchesFirstDivergenceWhenProtocolAgrees(t *testing.T) {
	a := baseCompareScenario()
	b := baseCompareScenario()
	b.Targets = []TargetProfile{{Name: "different", ServiceTime: 999 * time.Millisecond}}

	resultA, err := RunWorld(a, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(a) failed: %v", err)
	}
	resultB, err := RunWorld(b, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(b) failed: %v", err)
	}

	wantIdx, wantDiverged := FirstDivergence(resultA.Trace, resultB.Trace)
	gotIdx, gotDiverged, err := ComparePolicies(a, b, resultA.Trace, resultB.Trace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotIdx != wantIdx || gotDiverged != wantDiverged {
		t.Fatalf("ComparePolicies(%d,%v) != FirstDivergence(%d,%v)", gotIdx, gotDiverged, wantIdx, wantDiverged)
	}
}
