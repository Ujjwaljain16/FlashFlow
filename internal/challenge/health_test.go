package challenge

import (
	"testing"

	"flashflow/internal/clock"
	"flashflow/internal/health"
)

// TestHealthChallenge_FlappingBelowThresholdNeverTrips confirms a
// target whose probes alternate pass/fail -- flapping, but never
// accumulating enough CONSECUTIVE failures to cross
// UnhealthyFailThreshold -- never leaves HEALTHY. This is the
// robustness property the consecutive-counter design exists for: a
// noisy but not-actually-failing target shouldn't be excluded from
// traffic just because it's flapping.
func TestHealthChallenge_FlappingBelowThresholdNeverTrips(t *testing.T) {
	reg := health.NewRegistry(clock.NewMockClock(0), health.DefaultConfig()) // UnhealthyFailThreshold=2
	reg.RegisterTarget("t")

	for i := 0; i < 20; i++ {
		success := i%2 == 0 // perfect alternation: never two consecutive fails
		state := reg.RecordProbeResult("t", success)
		if state != health.StateHealthy {
			t.Fatalf("probe %d (success=%v): expected to remain HEALTHY under pure alternation, got %s", i, success, state)
		}
	}
}

// TestHealthChallenge_FlappingAtExactThresholdTrips is the companion
// case: a flapping pattern that DOES occasionally reach exactly the
// threshold must still trip UNHEALTHY at that point -- flapping
// resilience must not become blindness to a target that's genuinely
// bad often enough.
func TestHealthChallenge_FlappingAtExactThresholdTrips(t *testing.T) {
	reg := health.NewRegistry(clock.NewMockClock(0), health.DefaultConfig()) // UnhealthyFailThreshold=2
	reg.RegisterTarget("t")

	if s := reg.RecordProbeResult("t", false); s != health.StateHealthy {
		t.Fatalf("1st fail: expected still HEALTHY (threshold=2), got %s", s)
	}
	if s := reg.RecordProbeResult("t", false); s != health.StateUnhealthy {
		t.Fatalf("2nd consecutive fail: expected UNHEALTHY, got %s", s)
	}
}

// TestHealthChallenge_RecoveringSurvivesASingleFail documents a real,
// deliberate consequence of the consecutive-counter design: once a
// target is RECOVERING, a single fail does NOT immediately revert it
// to UNHEALTHY -- ConsecutiveFails resets to 1 (not enough to retrip a
// threshold of 2), so it takes two consecutive fails again, exactly the
// same bar as from HEALTHY. This is a real "delayed detection" property
// worth a permanent regression test, not an assumption.
func TestHealthChallenge_RecoveringSurvivesASingleFail(t *testing.T) {
	reg := health.NewRegistry(clock.NewMockClock(0), health.DefaultConfig())
	reg.RegisterTarget("t")

	reg.RecordProbeResult("t", false)
	if s := reg.RecordProbeResult("t", false); s != health.StateUnhealthy {
		t.Fatalf("expected UNHEALTHY after 2 consecutive fails, got %s", s)
	}
	if s := reg.RecordProbeResult("t", true); s != health.StateRecovering {
		t.Fatalf("expected RECOVERING after first pass from UNHEALTHY, got %s", s)
	}
	if s := reg.RecordProbeResult("t", false); s != health.StateRecovering {
		t.Fatalf("expected a single fail while RECOVERING to NOT immediately retrip UNHEALTHY, got %s", s)
	}
	if s := reg.RecordProbeResult("t", false); s != health.StateUnhealthy {
		t.Fatalf("expected a second consecutive fail while RECOVERING to retrip UNHEALTHY, got %s", s)
	}
}

// TestHealthChallenge_SimultaneousMultiTargetFailure confirms two
// targets failing in the identical probe round transition
// independently and correctly -- no shared state between per-target
// counters, and no ordering dependency in which target is recorded
// first within the round.
func TestHealthChallenge_SimultaneousMultiTargetFailure(t *testing.T) {
	reg := health.NewRegistry(clock.NewMockClock(0), health.DefaultConfig())
	reg.RegisterTarget("a")
	reg.RegisterTarget("b")

	// Round 1: both fail once.
	reg.RecordProbeResult("a", false)
	reg.RecordProbeResult("b", false)
	// Round 2: both fail again -- both should independently trip.
	sa := reg.RecordProbeResult("a", false)
	sb := reg.RecordProbeResult("b", false)

	if sa != health.StateUnhealthy {
		t.Fatalf("target a: expected UNHEALTHY after 2 consecutive fails, got %s", sa)
	}
	if sb != health.StateUnhealthy {
		t.Fatalf("target b: expected UNHEALTHY after 2 consecutive fails, got %s", sb)
	}

	// A third target that never failed must be entirely unaffected.
	reg.RegisterTarget("c")
	if !reg.IsAvailable("c") {
		t.Fatal("target c: an uninvolved target must remain available")
	}
	if reg.IsAvailable("a") || reg.IsAvailable("b") {
		t.Fatal("targets a and b: both must be unavailable after independently tripping UNHEALTHY")
	}
}

// TestHealthChallenge_DegradedCanStillBecomeUnhealthy confirms a target
// already DEGRADED (via live application error rate) is not "stuck"
// there -- probe failures still count toward UNHEALTHY, since being
// unreachable is a strictly worse condition than serving elevated
// errors.
func TestHealthChallenge_DegradedCanStillBecomeUnhealthy(t *testing.T) {
	reg := health.NewRegistry(clock.NewMockClock(0), health.DefaultConfig()) // DegradedErrorRate=0.20, MinAppRequestsForRate=10
	reg.RegisterTarget("t")

	for i := 0; i < 10; i++ {
		status := 200
		if i < 3 { // 3/10 = 30% error rate, over the 20% threshold
			status = 500
		}
		reg.RecordAppResult("t", status)
	}
	th, _ := reg.GetHealth("t")
	if th.State != health.StateDegraded {
		t.Fatalf("expected DEGRADED after a 30%% cumulative error rate, got %s", th.State)
	}

	reg.RecordProbeResult("t", false)
	if s := reg.RecordProbeResult("t", false); s != health.StateUnhealthy {
		t.Fatalf("expected DEGRADED -> UNHEALTHY after 2 consecutive probe fails, got %s", s)
	}
}
