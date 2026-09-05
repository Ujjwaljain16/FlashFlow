package challenge

import (
	"time"

	"testing"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

// TestReplayChallenge_LargeDivergenceAtTimeZero confirms FirstDivergence
// correctly reports divergence at the very first event when two
// scenarios differ from the start (completely different arrival
// schedules) -- the trivial-sounding boundary case a subtler
// off-by-one in divergence detection could still get wrong.
func TestReplayChallenge_LargeDivergenceAtTimeZero(t *testing.T) {
	a := replay.Scenario{
		Targets:  []replay.TargetProfile{{Name: "x", ServiceTime: 10 * time.Millisecond}},
		Arrivals: []replay.Arrival{{At: 0, Key: "/a"}},
	}
	b := replay.Scenario{
		Targets:  []replay.TargetProfile{{Name: "x", ServiceTime: 10 * time.Millisecond}},
		Arrivals: []replay.Arrival{{At: 0, Key: "/completely-different-key"}},
	}
	spec := replay.RoundRobinPolicy()

	resultA, err := replay.RunWorld(a, spec)
	if err != nil {
		t.Fatalf("RunWorld(a) failed: %v", err)
	}
	resultB, err := replay.RunWorld(b, spec)
	if err != nil {
		t.Fatalf("RunWorld(b) failed: %v", err)
	}
	idx, diverged := replay.FirstDivergence(resultA.Trace, resultB.Trace)
	if !diverged {
		t.Fatal("expected divergence: the two scenarios have different request keys from the very first event")
	}
	if idx != 0 {
		t.Fatalf("expected divergence at index 0 (the very first event), got %d", idx)
	}
}

// TestReplayChallenge_SimultaneousEventsAreDeterministic confirms two
// arrivals scheduled at the EXACT same virtual timestamp produce a
// stable, repeatable processing order -- the tie-break the underlying
// event queue uses (insertion order) must be deterministic across
// separate RunWorld calls, not an artifact of map iteration or
// goroutine scheduling that could vary run to run.
func TestReplayChallenge_SimultaneousEventsAreDeterministic(t *testing.T) {
	sc := replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "x", ServiceTime: 10 * time.Millisecond},
			{Name: "y", ServiceTime: 10 * time.Millisecond},
		},
		// Multiple arrivals at the identical virtual timestamp.
		Arrivals: []replay.Arrival{
			{At: 0, Key: "/a"}, {At: 0, Key: "/b"}, {At: 0, Key: "/c"}, {At: 0, Key: "/d"},
		},
	}
	spec := replay.RoundRobinPolicy()

	result1, err := replay.RunWorld(sc, spec)
	if err != nil {
		t.Fatalf("RunWorld (1st) failed: %v", err)
	}
	result2, err := replay.RunWorld(sc, spec)
	if err != nil {
		t.Fatalf("RunWorld (2nd) failed: %v", err)
	}
	if idx, diverged := replay.FirstDivergence(result1.Trace, result2.Trace); diverged {
		t.Fatalf("simultaneous-timestamp events processed in a different order across runs, diverging at event %d", idx)
	}
	if len(result1.Records) != 4 {
		t.Fatalf("expected all 4 simultaneous arrivals to be processed, got %d records", len(result1.Records))
	}
}

// TestReplayChallenge_EarlyDivergenceHasStatefulConsequences confirms
// an intervention early in a run doesn't just change a handful of trace
// events near the intervention point -- it propagates to different
// FINAL aggregate state (CompletedByTarget), the actual property a
// counterfactual comparison exists to measure. A replay engine that
// diverged in the trace but silently reconverged to the same final
// metrics would be far less useful than this project's experiments
// (007-F through 008-F) have assumed.
func TestReplayChallenge_EarlyDivergenceHasStatefulConsequences(t *testing.T) {
	const requests = 100
	// Spacing MUST exceed ServiceTime: if consecutive requests to the
	// same target overlap (spacing < service time), the target's own
	// in-flight Load accumulates and its utilization score collapses
	// independent of cache affinity or latency -- the exact "stuck load
	// counter" confound 007-D diagnosed. An earlier version of this
	// test used 5ms spacing against 10ms service time and found BOTH
	// scenarios converged to an identical 50/50 split regardless of the
	// intervention -- not because the intervention didn't matter, but
	// because load-driven oscillation between the two targets dominated
	// everything else in both scenarios equally, washing out the signal
	// this test exists to isolate. 15ms spacing against 10ms service
	// time lets each request fully complete before the next arrives.
	const spacing = 15 * time.Millisecond
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: "/x"} // single key: cache affinity locks in from request 1
	}

	base := replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "a", ServiceTime: 10 * time.Millisecond},
			{Name: "b", ServiceTime: 10 * time.Millisecond},
		},
		Arrivals: arrivals,
	}

	// Intervention: force the first several requests onto a specific
	// target by making the OTHER target unhealthy at the very start.
	// The outage MUST comfortably exceed detection latency: the default
	// health.Config requires 2 CONSECUTIVE probe failures
	// (UnhealthyFailThreshold=2) spaced by the 100ms probe interval
	// before a target is actually marked unavailable. An earlier
	// version of this test used a 50ms outage and found it had NO
	// effect on either scenario's final routing at all -- not because
	// health filtering was broken, but because the outage recovered
	// (at t=50ms) before the second probe (at t=100ms) could ever fire,
	// so "a" was never actually detected as down. 300ms comfortably
	// clears the ~200ms minimum detection time.
	withEarlyIntervention := base
	withEarlyIntervention.UseHealthRegistry = true
	withEarlyIntervention.Failures = []replay.FailureWindow{
		{Target: "a", DownAt: 0, UpAt: clock.VirtualTime(300 * time.Millisecond)},
	}
	withEarlyIntervention.Horizon = clock.VirtualTime(2 * time.Second)

	baseWithHealth := base
	baseWithHealth.UseHealthRegistry = true // identical health machinery, no failure -- isolates the intervention itself, per 007-F/world_test.go's own established discipline
	baseWithHealth.Horizon = clock.VirtualTime(2 * time.Second)

	spec := replay.AdaptivePolicy()
	resultBase, err := replay.RunWorld(baseWithHealth, spec)
	if err != nil {
		t.Fatalf("RunWorld (base) failed: %v", err)
	}
	resultIntervened, err := replay.RunWorld(withEarlyIntervention, spec)
	if err != nil {
		t.Fatalf("RunWorld (intervened) failed: %v", err)
	}

	if _, diverged := replay.FirstDivergence(resultBase.Trace, resultIntervened.Trace); !diverged {
		t.Fatal("expected the early health intervention to cause a trace divergence")
	}

	// The single shared key means cache affinity, once established,
	// persists for the whole run -- so which target answers the very
	// first request should determine which target serves nearly all
	// 100 requests, a large, easily-observed difference in final state.
	if resultBase.CompletedByTarget["a"] == resultIntervened.CompletedByTarget["a"] {
		t.Fatalf("expected the early intervention to produce a materially different final CompletedByTarget distribution, got identical counts in both: %v", resultBase.CompletedByTarget)
	}
}
