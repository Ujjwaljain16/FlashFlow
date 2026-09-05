package challenge

import (
	"testing"

	"flashflow/internal/replay"
	"flashflow/internal/vtime"
)

// detectAvailabilityWindow mirrors 007-G/008-F's own helper: scans a
// run's trace for a target's health_probe entries and returns the
// virtual times it was first detected unhealthy and next detected
// healthy again.
func detectAvailabilityWindow(trace []vtime.TraceEvent, target string) (unhealthyAt, healthyAgainAt float64) {
	available := true
	unhealthyAt, healthyAgainAt = -1, -1
	for _, ev := range trace {
		if ev.Type != "health_probe" || ev.Entity != target {
			continue
		}
		state, _ := ev.Fields["state"].(string)
		nowAvailable := state == "HEALTHY" || state == "DEGRADED"
		if nowAvailable != available {
			t := float64(ev.Time) / 1e6
			if !nowAvailable && unhealthyAt < 0 {
				unhealthyAt = t
			} else if nowAvailable && unhealthyAt >= 0 && healthyAgainAt < 0 {
				healthyAgainAt = t
			}
			available = nowAvailable
		}
	}
	return unhealthyAt, healthyAgainAt
}

// TestGoldenScenario_NoUnhealthyTargetSelected is the invariant master
// context rule 38 names first: no unhealthy target is ever selected.
// Checked for every policy that can run this scenario, not just
// Adaptive -- 007-G/008-F already established health eligibility is a
// property of upstream filtering, not any one selector, and this
// permanent test is what would catch a regression in that filtering
// itself.
func TestGoldenScenario_NoUnhealthyTargetSelected(t *testing.T) {
	sc := GoldenScenario()
	policies := []replay.PolicySpec{
		replay.RoundRobinPolicy(), replay.WeightedRoundRobinPolicy(), replay.LeastConnectionsPolicy(),
		replay.EWMAPolicy(), replay.P2CLoadPolicy(), replay.AdaptivePolicy(),
	}
	for _, spec := range policies {
		result, err := replay.RunWorld(sc, spec)
		if err != nil {
			t.Fatalf("%s: RunWorld failed: %v", spec.Name, err)
		}
		unhealthyAt, healthyAgainAt := detectAvailabilityWindow(result.Trace, "edge-b")
		if unhealthyAt < 0 || healthyAgainAt < 0 {
			t.Fatalf("%s: expected edge-b's failure/recovery to be detected in the trace, got unhealthyAt=%v healthyAgainAt=%v", spec.Name, unhealthyAt, healthyAgainAt)
		}
		for _, rec := range result.Records {
			if rec.Target == "edge-b" && rec.VirtualTimeMs > unhealthyAt && rec.VirtualTimeMs < healthyAgainAt {
				t.Fatalf("%s: edge-b selected at t=%.1fms, inside its confirmed-unhealthy window [%.1f, %.1f)", spec.Name, rec.VirtualTimeMs, unhealthyAt, healthyAgainAt)
			}
		}
	}
}

// TestGoldenScenario_TraceIsDeterministic is the second invariant:
// running the identical golden scenario through the identical policy
// twice must produce a byte-for-byte identical trace. This is the
// permanent regression check for the determinism guarantee every
// experiment since Stage 5 has depended on, anchored to one fixed,
// version-controlled scenario rather than a scenario built fresh by
// each test that happens to check it.
func TestGoldenScenario_TraceIsDeterministic(t *testing.T) {
	sc := GoldenScenario()
	spec := replay.AdaptivePolicy()

	result1, err := replay.RunWorld(sc, spec)
	if err != nil {
		t.Fatalf("RunWorld (1st) failed: %v", err)
	}
	result2, err := replay.RunWorld(sc, spec)
	if err != nil {
		t.Fatalf("RunWorld (2nd) failed: %v", err)
	}
	if idx, diverged := replay.FirstDivergence(result1.Trace, result2.Trace); diverged {
		t.Fatalf("identical golden scenario runs diverged at event %d: %+v vs %+v", idx, result1.Trace[idx], result2.Trace[idx])
	}
}

// TestGoldenScenario_CounterfactualIsolation is the third invariant:
// counterfactual worlds evaluated against the golden scenario must
// remain isolated from each other. internal/replay/world_test.go
// already proves this generically; this test anchors the same property
// specifically to the one scenario this project treats as canonical,
// so a regression here is caught against a scenario every contributor
// recognizes, not only a synthetic fixture.
func TestGoldenScenario_CounterfactualIsolation(t *testing.T) {
	sc := GoldenScenario()
	adaptiveSpec := replay.AdaptivePolicy()

	before, err := replay.RunWorld(sc, adaptiveSpec)
	if err != nil {
		t.Fatalf("RunWorld (before) failed: %v", err)
	}

	// A different policy, run against the identical scenario in
	// between -- if any state leaked across RunWorld calls, this is
	// where it would show up.
	if _, err := replay.RunWorld(sc, replay.EWMAPolicy()); err != nil {
		t.Fatalf("RunWorld (interleaved EWMA) failed: %v", err)
	}

	after, err := replay.RunWorld(sc, adaptiveSpec)
	if err != nil {
		t.Fatalf("RunWorld (after) failed: %v", err)
	}

	if idx, diverged := replay.FirstDivergence(before.Trace, after.Trace); diverged {
		t.Fatalf("running a different policy against the golden scenario in between changed Adaptive's own outcome at event %d -- state leaked across RunWorld calls", idx)
	}
}
