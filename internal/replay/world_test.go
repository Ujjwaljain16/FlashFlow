package replay

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
)

// erroringSelector fails SelectTarget for any request whose key is in
// failKeys, and falls back to plain round-robin otherwise -- used to force
// deterministic, independent scheduling failures without needing a
// real-world condition to trigger one.
type erroringSelector struct {
	failKeys map[string]bool
	next     int
}

func (s *erroringSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if s.failKeys[r.URL.Path] {
		return "", errors.New("forced selection failure for test")
	}
	if len(available) == 0 {
		return "", proxy.ErrNoHealthyTargets
	}
	t := available[s.next%len(available)]
	s.next++
	return t, nil
}

func heterogeneousScenario(seed int64) Scenario {
	const spacing = 5 * time.Millisecond
	const n = 100
	arrivals := make([]Arrival, n)
	for i := 0; i < n; i++ {
		key := "/hot"
		if i%2 != 0 {
			key = "/cold"
		}
		arrivals[i] = Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: key}
	}
	return Scenario{
		Targets: []TargetProfile{
			{Name: "slow", ServiceTime: 100 * time.Millisecond},
			{Name: "fast-a", ServiceTime: 20 * time.Millisecond},
			{Name: "fast-b", ServiceTime: 20 * time.Millisecond},
		},
		Arrivals: arrivals,
		Seeds:    DeriveSeeds(seed),
	}
}

func TestRunWorld_NoFailures_EverySelectionAccountedFor(t *testing.T) {
	scenario := heterogeneousScenario(1)
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Records) != len(scenario.Arrivals) {
		t.Fatalf("expected %d selections (no failures, nothing should be rejected), got %d", len(scenario.Arrivals), len(result.Records))
	}
	if result.RejectedCount != 0 {
		t.Fatalf("expected 0 rejections with no failures, got %d", result.RejectedCount)
	}
	total := 0
	for _, n := range result.CompletedByTarget {
		total += n
	}
	if total != len(scenario.Arrivals) {
		t.Fatalf("expected every arrival to eventually complete, got %d completions for %d arrivals", total, len(scenario.Arrivals))
	}
}

// TestRunWorld_IdentityDeterministic is the identity property the whole
// package rests on: the same Scenario (including Seed) run through the
// same PolicySpec twice must produce byte-for-byte identical output, down
// to the full causal Trace, not just the summary. P2CLoadPolicy is the
// deliberate choice here -- it is the one policy whose behavior depends
// on external randomness, so this test also confirms that randomness is
// seeded from the Scenario, not read from any shared/global source.
func TestRunWorld_IdentityDeterministic(t *testing.T) {
	scenario := heterogeneousScenario(42)
	spec := P2CLoadPolicy()

	result1, err := RunWorld(scenario, spec)
	if err != nil {
		t.Fatalf("RunWorld (1st) failed: %v", err)
	}
	result2, err := RunWorld(scenario, spec)
	if err != nil {
		t.Fatalf("RunWorld (2nd) failed: %v", err)
	}

	if idx, diverged := FirstDivergence(result1.Trace, result2.Trace); diverged {
		t.Fatalf("identical Scenario+PolicySpec produced diverging traces at event %d: %+v vs %+v",
			idx, result1.Trace[idx], result2.Trace[idx])
	}
	if !reflect.DeepEqual(result1.Records, result2.Records) {
		t.Fatalf("identical Scenario+PolicySpec produced diverging selection records:\n%+v\nvs\n%+v", result1.Records, result2.Records)
	}
	if !reflect.DeepEqual(result1.CompletedByTarget, result2.CompletedByTarget) {
		t.Fatalf("identical Scenario+PolicySpec produced diverging completion counts: %+v vs %+v", result1.CompletedByTarget, result2.CompletedByTarget)
	}
	// Completions (added for Stage 8's tuning objective, F-44 regression):
	// added alongside Records/CompletedByTarget but not originally
	// covered by this identity check -- a pure function of the
	// deterministic engine clock, so it should already be
	// identity-stable, but this test existing is what makes that a
	// checked property instead of an assumption.
	if !reflect.DeepEqual(result1.Completions, result2.Completions) {
		t.Fatalf("identical Scenario+PolicySpec produced diverging completion records:\n%+v\nvs\n%+v", result1.Completions, result2.Completions)
	}
}

// TestRunWorld_DivergenceOnlyAfterInterventionPoint is the causality
// half of the counterfactual claim: two Scenarios that share identical
// history up to a cutoff, then differ only in a failure introduced after
// it, must produce traces that are identical up to the cutoff and
// diverge starting there -- never earlier (a later change leaking
// backward into the past would be a real causality bug) and never
// "eventually, nowhere in particular" (too weak to trust as evidence the
// intervention did anything at all).
func TestRunWorld_DivergenceOnlyAfterInterventionPoint(t *testing.T) {
	base := heterogeneousScenario(7)
	const cutoff = clock.VirtualTime(200 * time.Millisecond)

	// Both variants force UseHealthRegistry so they run identical
	// health-probe machinery regardless of whether a failure ever
	// happens -- otherwise the mere presence/absence of health_probe
	// trace events would itself cause an immediate, meaningless
	// divergence at t=0, before the actual intervention this test cares
	// about. A Horizon is required whenever a registry is built: its
	// probe Ticker never stops itself, so RunUntilEmpty would never
	// return. 2s comfortably covers both the last arrival's completion
	// and the failure's UpAt.
	base.UseHealthRegistry = true
	base.Horizon = clock.VirtualTime(2 * time.Second)

	withoutFailure := base
	withFailure := base
	withFailure.Failures = []FailureWindow{
		{Target: "fast-a", DownAt: cutoff, UpAt: cutoff.Add(1 * time.Second)},
	}

	spec := AdaptivePolicy()
	resultA, err := RunWorld(withoutFailure, spec)
	if err != nil {
		t.Fatalf("RunWorld (no failure) failed: %v", err)
	}
	resultB, err := RunWorld(withFailure, spec)
	if err != nil {
		t.Fatalf("RunWorld (with failure) failed: %v", err)
	}

	idx, diverged := FirstDivergence(resultA.Trace, resultB.Trace)
	if !diverged {
		t.Fatal("expected the two scenarios to diverge once the failure takes effect, but traces were identical")
	}
	if resultA.Trace[idx].Time < cutoff {
		t.Fatalf("traces diverged at t=%v, before the intervention at t=%v -- a later change is visible in the past",
			resultA.Trace[idx].Time, cutoff)
	}

	// Everything strictly before the cutoff must be untouched by a
	// change that hasn't happened yet.
	for i := 0; i < idx; i++ {
		if resultA.Trace[i].Time >= cutoff {
			break
		}
		if !reflect.DeepEqual(resultA.Trace[i], resultB.Trace[i]) {
			t.Fatalf("event %d (t=%v, before cutoff t=%v) differs between scenarios: %+v vs %+v",
				i, resultA.Trace[i].Time, cutoff, resultA.Trace[i], resultB.Trace[i])
		}
	}
}

// TestRunWorld_Isolation confirms that running an unrelated Scenario
// between two runs of the same Scenario+PolicySpec cannot change the
// second run's outcome. This is the test that would catch an accidental
// shared/global mutable object (e.g. a package-level rand source instead
// of one constructed fresh per RunWorld call) that no amount of reading
// RunWorld's code guarantees is absent.
func TestRunWorld_Isolation(t *testing.T) {
	scenario := heterogeneousScenario(99)
	spec := P2CLoadPolicy()

	before, err := RunWorld(scenario, spec)
	if err != nil {
		t.Fatalf("RunWorld (before) failed: %v", err)
	}

	unrelated := heterogeneousScenario(12345)
	unrelated.Failures = []FailureWindow{{Target: "slow", DownAt: 0, UpAt: clock.VirtualTime(1 * time.Second)}}
	unrelated.Horizon = clock.VirtualTime(2 * time.Second) // Failures starts a never-stopping probe Ticker; see the comment above.
	if _, err := RunWorld(unrelated, AdaptivePolicy()); err != nil {
		t.Fatalf("RunWorld (unrelated, interleaved) failed: %v", err)
	}

	after, err := RunWorld(scenario, spec)
	if err != nil {
		t.Fatalf("RunWorld (after) failed: %v", err)
	}

	if idx, diverged := FirstDivergence(before.Trace, after.Trace); diverged {
		t.Fatalf("running an unrelated scenario in between changed this scenario's outcome at event %d: %+v vs %+v -- shared state leaked between runs",
			idx, before.Trace[idx], after.Trace[idx])
	}
	if !reflect.DeepEqual(before.Completions, after.Completions) {
		t.Fatalf("running an unrelated scenario in between changed this scenario's Completions -- shared state leaked between runs:\n%+v\nvs\n%+v", before.Completions, after.Completions)
	}
}

// TestRunWorld_MultipleSchedulingFailures_AllReportedAndPartialResultsKept
// regression-tests F-28: scheduleErr was a single shared variable
// overwritten by whichever failing arrival's closure ran last, and once
// set, the entire WorldResult was discarded (a bare zero value returned)
// even though most arrivals scheduled and completed successfully.
func TestRunWorld_MultipleSchedulingFailures_AllReportedAndPartialResultsKept(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{Name: "only", ServiceTime: 5 * time.Millisecond}},
		Arrivals: []Arrival{
			{At: 0, Key: "/ok-1"},
			{At: clock.VirtualTime((10 * time.Millisecond).Nanoseconds()), Key: "/fail-a"},
			{At: clock.VirtualTime((20 * time.Millisecond).Nanoseconds()), Key: "/ok-2"},
			{At: clock.VirtualTime((30 * time.Millisecond).Nanoseconds()), Key: "/fail-b"},
			{At: clock.VirtualTime((40 * time.Millisecond).Nanoseconds()), Key: "/ok-3"},
		},
		Seeds: DeriveSeeds(1),
	}
	spec := PolicySpec{
		Name: "erroring-test-selector",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			return &erroringSelector{failKeys: map[string]bool{"/fail-a": true, "/fail-b": true}}, NoInstrumentation{}
		},
	}

	result, err := RunWorld(scenario, spec)
	if err == nil {
		t.Fatalf("expected a non-nil error from two forced selection failures")
	}

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("expected an errors.Join-style joined error, got %T: %v", err, err)
	}
	if got := len(joined.Unwrap()); got != 2 {
		t.Fatalf("expected exactly 2 joined errors (one per forced failure), got %d: %v", got, err)
	}

	// The 3 successful arrivals must still be visible, not discarded
	// because the other 2 failed.
	if len(result.Records) != 3 {
		t.Fatalf("expected 3 successful selections preserved despite 2 failures, got %d", len(result.Records))
	}
	if len(result.Completions) != 3 {
		t.Fatalf("expected 3 completions preserved despite 2 failures, got %d", len(result.Completions))
	}
}

// TestRunWorld_HorizonTruncation_InFlightRequestsAreCounted
// regression-tests F-29: a request dispatched before Horizon but whose
// service time completes after it used to vanish from every count
// (neither completed nor rejected) with no way to tell the run was
// truncated at all.
func TestRunWorld_HorizonTruncation_InFlightRequestsAreCounted(t *testing.T) {
	scenario := Scenario{
		Targets:  []TargetProfile{{Name: "slow", ServiceTime: 500 * time.Millisecond}},
		Arrivals: []Arrival{{At: 0, Key: "/late"}},
		// Horizon cuts off well before the 500ms service time elapses --
		// the request is dispatched (a real SelectionRecord exists) but
		// never completes within this run.
		Horizon: clock.VirtualTime((50 * time.Millisecond).Nanoseconds()),
		Seeds:   DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected the request to be dispatched (1 record), got %d", len(result.Records))
	}
	if len(result.Completions) != 0 {
		t.Fatalf("expected 0 completions before Horizon truncation, got %d", len(result.Completions))
	}
	if result.InFlightAtHorizon != 1 {
		t.Fatalf("expected InFlightAtHorizon=1 for the truncated request, got %d", result.InFlightAtHorizon)
	}
}
