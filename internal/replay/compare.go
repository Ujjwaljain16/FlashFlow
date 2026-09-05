package replay

import (
	"errors"
	"reflect"

	"flashflow/internal/vtime"
)

// ErrProtocolMismatch is returned by ComparePolicies when the two
// Scenarios being compared disagree on a protocol field (see
// Scenario.SameProtocol) — a divergence found between their traces in
// that case cannot be attributed to a policy difference alone.
var ErrProtocolMismatch = errors.New("replay: scenarios differ in protocol fields (UseHealthRegistry/ProbeInterval/Horizon) — a divergence between their traces would not be attributable to a policy difference alone")

// FirstDivergence returns the index of the first TraceEvent at which a
// and b differ, and true. If one trace is a prefix of the other, the
// shorter trace's length is returned. If the traces are identical, it
// returns (0, false).
//
// This exists specifically for divergence tests: two Scenarios built to
// share identical history up to some cutoff time should produce traces
// that are byte-for-byte equal up to that point and differ starting
// exactly there -- not earlier (which would mean a later change is
// somehow visible in the past, a causality bug) and not "eventually,
// somewhere" (which would be too weak a claim to trust).
func FirstDivergence(a, b []vtime.TraceEvent) (int, bool) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if !reflect.DeepEqual(a[i], b[i]) {
			return i, true
		}
	}
	if len(a) != len(b) {
		return n, true
	}
	return 0, false
}

// ComparePolicies is FirstDivergence with the exogenous/protocol
// precondition enforced rather than merely relied upon by convention: it
// first checks scenarioA.SameProtocol(scenarioB) and returns
// ErrProtocolMismatch if they disagree, since a "divergence" between two
// Scenarios that differ in Horizon/ProbeInterval/UseHealthRegistry alone
// would be a run-length or health-machinery artifact, not something
// attributable to whatever policy difference the caller actually means
// to compare. Callers that have already independently verified their
// Scenario pair matches protocol (e.g. by construction) may still call
// FirstDivergence directly; this wrapper is for call sites that want the
// guardrail checked, not just assumed.
func ComparePolicies(scenarioA, scenarioB Scenario, traceA, traceB []vtime.TraceEvent) (index int, diverged bool, err error) {
	if !scenarioA.SameProtocol(scenarioB) {
		return 0, false, ErrProtocolMismatch
	}
	index, diverged = FirstDivergence(traceA, traceB)
	return index, diverged, nil
}
