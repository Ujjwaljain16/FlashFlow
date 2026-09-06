package replay

import (
	"testing"
	"time"
)

// Stage 12 Track C validation: discrete, scheduled changes to a target's
// ServiceTime (docs/StageArtifacts/Stage12.md). Built specifically to
// answer Stage 11's disclosed gap (docs/StageArtifacts/Stage11.md §12):
// H2 (a latency-oscillation/staleness attack) could not be tested because
// ServiceTime was fixed for an entire Scenario.

// TestServiceTimeSchedule_BeforeAfterRecovery is the core semantic: a
// request whose service begins before a change keeps its original
// ServiceTime; one whose service begins after observes the new value;
// one whose service begins after a later change back observes the
// recovered value.
func TestServiceTimeSchedule_BeforeAfterRecovery(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{
			Name: "t1", ServiceTime: 15 * time.Millisecond,
			ServiceTimeSchedule: []ServiceTimeChange{
				{At: msVT(50 * time.Millisecond), NewServiceTime: 120 * time.Millisecond},
				{At: msVT(200 * time.Millisecond), NewServiceTime: 15 * time.Millisecond},
			},
		}},
		Arrivals: []Arrival{
			{At: msVT(10 * time.Millisecond), Key: "/before"},     // dispatches at 10ms, well before the 50ms change -- original 15ms
			{At: msVT(60 * time.Millisecond), Key: "/degraded"},   // dispatches at 60ms, after the 50ms change -- degraded 120ms
			{At: msVT(210 * time.Millisecond), Key: "/recovered"}, // dispatches at 210ms, after the 200ms recovery -- back to 15ms
		},
		Horizon: msVT(500 * time.Millisecond),
		Seeds:   DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 3 {
		t.Fatalf("expected 3 completions, got %d", len(result.Completions))
	}
	before, degraded, recovered := result.Completions[0], result.Completions[1], result.Completions[2]
	if before.Latency != 15*time.Millisecond {
		t.Errorf("before the change: expected 15ms, got %v", before.Latency)
	}
	if degraded.Latency != 120*time.Millisecond {
		t.Errorf("after degrading: expected 120ms, got %v", degraded.Latency)
	}
	if recovered.Latency != 15*time.Millisecond {
		t.Errorf("after recovery: expected 15ms again, got %v", recovered.Latency)
	}
}

// TestServiceTimeSchedule_InFlightRequestUnaffectedByLaterChange proves a
// request already in service is NOT retroactively affected by a change
// that occurs mid-service: dispatched just before a change to a much
// larger service time, it must still complete using its ORIGINAL value.
func TestServiceTimeSchedule_InFlightRequestUnaffectedByLaterChange(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{
			Name: "t1", ServiceTime: 20 * time.Millisecond,
			ServiceTimeSchedule: []ServiceTimeChange{
				{At: msVT(5 * time.Millisecond), NewServiceTime: 500 * time.Millisecond},
			},
		}},
		// Dispatched at t=0 with 20ms service -- already scheduled to
		// complete at t=20ms BEFORE the t=5ms change even fires. If the
		// change retroactively affected in-flight requests, this would
		// instead complete at some much later time (>= 500ms).
		Arrivals: []Arrival{{At: 0, Key: "/in-flight"}},
		Horizon:  msVT(1 * time.Second),
		Seeds:    DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(result.Completions))
	}
	if result.Completions[0].Latency != 20*time.Millisecond {
		t.Errorf("expected the in-flight request to keep its original 20ms service time, unaffected by the later 500ms change, got %v", result.Completions[0].Latency)
	}
}

// TestServiceTimeSchedule_ChangeAtScenarioStartAppliesBeforeFirstArrival
// defines the t=0 tie-break explicitly: a change scheduled AT t=0 must
// apply before a t=0 arrival is processed (RunWorld schedules every
// ServiceTimeChange before any arrival, so vtime's own insertion-order
// tie-break resolves this deterministically).
func TestServiceTimeSchedule_ChangeAtScenarioStartAppliesBeforeFirstArrival(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{
			Name: "t1", ServiceTime: 15 * time.Millisecond,
			ServiceTimeSchedule: []ServiceTimeChange{{At: 0, NewServiceTime: 99 * time.Millisecond}},
		}},
		Arrivals: []Arrival{{At: 0, Key: "/x"}},
		Horizon:  msVT(1 * time.Second),
		Seeds:    DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(result.Completions))
	}
	if result.Completions[0].Latency != 99*time.Millisecond {
		t.Errorf("expected a t=0 change to apply before a t=0 arrival (99ms), got %v", result.Completions[0].Latency)
	}
}

// TestServiceTimeSchedule_MultipleChangesSameTimestampDeterministic: two
// changes to the SAME target at the SAME timestamp must resolve
// deterministically (last one registered wins, per vtime's insertion-
// sequence tie-break) -- and must do so identically across repeated runs.
func TestServiceTimeSchedule_MultipleChangesSameTimestampDeterministic(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{
			Name: "t1", ServiceTime: 10 * time.Millisecond,
			ServiceTimeSchedule: []ServiceTimeChange{
				{At: msVT(5 * time.Millisecond), NewServiceTime: 50 * time.Millisecond},
				{At: msVT(5 * time.Millisecond), NewServiceTime: 77 * time.Millisecond},
			},
		}},
		Arrivals: []Arrival{{At: msVT(6 * time.Millisecond), Key: "/x"}},
		Horizon:  msVT(1 * time.Second),
		Seeds:    DeriveSeeds(1),
	}
	a, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(a) failed: %v", err)
	}
	b, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(b) failed: %v", err)
	}
	if len(a.Completions) != 1 || len(b.Completions) != 1 {
		t.Fatalf("expected 1 completion each, got %d and %d", len(a.Completions), len(b.Completions))
	}
	if a.Completions[0].Latency != b.Completions[0].Latency {
		t.Fatalf("same-timestamp service-time changes produced nondeterministic results across runs: %v vs %v", a.Completions[0].Latency, b.Completions[0].Latency)
	}
	// The registration order in ServiceTimeSchedule (50ms, then 77ms) is
	// the insertion order RunWorld schedules them in -- vtime's tie-break
	// applies the LAST-inserted event's effect last, so 77ms wins.
	if a.Completions[0].Latency != 77*time.Millisecond {
		t.Errorf("expected the later-registered same-timestamp change (77ms) to win deterministically, got %v", a.Completions[0].Latency)
	}
}

// TestServiceTimeSchedule_ReplayCounterfactualIsolation: two policies run
// against the IDENTICAL Scenario (including its ServiceTimeSchedule) must
// see the identical schedule of changes -- exogenous physics, unaffected
// by which policy is being evaluated, exactly like ServiceTime itself.
func TestServiceTimeSchedule_ReplayCounterfactualIsolation(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{
			{Name: "fast", ServiceTime: 10 * time.Millisecond, ServiceTimeSchedule: []ServiceTimeChange{
				{At: msVT(20 * time.Millisecond), NewServiceTime: 200 * time.Millisecond},
			}},
			{Name: "slow", ServiceTime: 50 * time.Millisecond},
		},
		Arrivals: []Arrival{
			{At: 0, Key: "/a"},
			{At: msVT(5 * time.Millisecond), Key: "/b"},
			{At: msVT(30 * time.Millisecond), Key: "/c"}, // after fast's degradation
		},
		Horizon: msVT(1 * time.Second),
		Seeds:   DeriveSeeds(1),
	}
	rr, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(round-robin) failed: %v", err)
	}
	ewma, err := RunWorld(scenario, EWMAPolicy())
	if err != nil {
		t.Fatalf("RunWorld(ewma) failed: %v", err)
	}
	// Not asserting identical outcomes (different policies SHOULD route
	// differently) -- asserting the schedule itself wasn't mutated by one
	// run in a way that would leak into the other: the Scenario value is
	// the same Go value passed to both calls, and TargetProfile.
	// ServiceTimeSchedule is never mutated in place by RunWorld (only a
	// local, per-call serviceTimes MAP entry is updated) -- confirmed
	// here by checking the ORIGINAL scenario's own field is untouched.
	if len(scenario.Targets[0].ServiceTimeSchedule) != 1 || scenario.Targets[0].ServiceTimeSchedule[0].NewServiceTime != 200*time.Millisecond {
		t.Fatalf("the Scenario's own ServiceTimeSchedule was mutated by a RunWorld call -- exogenous input contamination")
	}
	if len(rr.Completions) == 0 || len(ewma.Completions) == 0 {
		t.Fatal("expected both policies to complete at least some requests")
	}
}
