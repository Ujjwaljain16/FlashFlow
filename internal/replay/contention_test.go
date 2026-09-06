package replay

import (
	"testing"
	"time"

	"flashflow/internal/clock"
)

// Stage 12 Track D validation: a minimal, deterministic finite-capacity
// contention model (docs/StageArtifacts/Stage12.md). Each case below is
// analytically hand-computable -- these are NOT stochastic queueing-
// theory (M/M/c) validations, they are direct checks of this specific
// deterministic discrete-event implementation's arithmetic.

func msVT(d time.Duration) clock.VirtualTime { return clock.VirtualTime(d.Nanoseconds()) }

// Case A: infinite capacity (Capacity's zero value) must reproduce the
// pre-Stage-12 flat model exactly -- every request's latency equals its
// own fixed ServiceTime, regardless of how many other requests overlap.
func TestContention_InfiniteCapacityMatchesFlatModel(t *testing.T) {
	scenario := Scenario{
		Targets:  []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond}}, // Capacity: 0 (unset) == infinite
		Arrivals: []Arrival{{At: 0, Key: "/a"}, {At: msVT(1 * time.Millisecond), Key: "/b"}},
		Horizon:  msVT(50 * time.Millisecond),
		Seeds:    DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(result.Completions))
	}
	for _, c := range result.Completions {
		if c.Latency != 10*time.Millisecond {
			t.Errorf("expected latency exactly 10ms (no queueing under infinite capacity), got %v", c.Latency)
		}
	}
}

// Case B: one slot. Two requests, arriving before the first would
// complete, must serialize -- the second's total latency includes its
// full wait for the first to finish.
func TestContention_OneSlot_SequentialWaiting(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 1}},
		Arrivals: []Arrival{
			{At: 0, Key: "/a"},
			{At: msVT(1 * time.Millisecond), Key: "/b"},
		},
		Horizon: msVT(100 * time.Millisecond),
		Seeds:   DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(result.Completions))
	}
	// Completions preserve dispatch order under a single target with a
	// fixed service time (FIFO, no request can overtake an earlier one).
	first, second := result.Completions[0], result.Completions[1]
	if first.Latency != 10*time.Millisecond {
		t.Errorf("expected the first request's latency to be exactly its service time (10ms, no wait), got %v", first.Latency)
	}
	// Second request: dispatched at t=1ms, must wait until t=10ms (when
	// the first completes) before its own 10ms service begins, completing
	// at t=20ms -- total latency 20-1=19ms.
	if second.Latency != 19*time.Millisecond {
		t.Errorf("expected the second request's latency to be 19ms (1ms until dispatch + 9ms remaining wait + 10ms service = 19ms from ITS OWN dispatch), got %v", second.Latency)
	}
}

// Case C: two slots. Two requests overlap freely; a third, arriving
// while both slots are occupied, must wait for the first to free one.
func TestContention_TwoSlots_ThirdRequestWaits(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 2}},
		Arrivals: []Arrival{
			{At: 0, Key: "/a"},
			{At: msVT(1 * time.Millisecond), Key: "/b"},
			{At: msVT(2 * time.Millisecond), Key: "/c"},
		},
		Horizon: msVT(100 * time.Millisecond),
		Seeds:   DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 3 {
		t.Fatalf("expected 3 completions, got %d", len(result.Completions))
	}
	a, b, c := result.Completions[0], result.Completions[1], result.Completions[2]
	if a.Latency != 10*time.Millisecond {
		t.Errorf("request a: expected 10ms (immediate service, slot 1), got %v", a.Latency)
	}
	if b.Latency != 10*time.Millisecond {
		t.Errorf("request b: expected 10ms (immediate service, slot 2, both slots occupied concurrently), got %v", b.Latency)
	}
	// c arrives at t=2ms with both slots busy, queues; a completes at
	// t=10ms freeing a slot, c begins service then, completes at t=20ms:
	// total latency 20-2=18ms.
	if c.Latency != 18*time.Millisecond {
		t.Errorf("request c: expected 18ms (2ms until dispatch + 8ms wait for a's slot to free + 10ms service), got %v", c.Latency)
	}
}

// Case D: deliberate overload. Arrivals faster than one slot can drain
// must show monotonically non-decreasing latency (later arrivals wait
// longer, as queue depth grows) and every request must eventually
// complete -- queued work is never lost.
func TestContention_Overload_LatencyGrowsMonotonically(t *testing.T) {
	const n = 6
	arrivals := make([]Arrival, n)
	for i := 0; i < n; i++ {
		arrivals[i] = Arrival{At: msVT(time.Duration(i) * 5 * time.Millisecond), Key: "/x"}
	}
	scenario := Scenario{
		Targets:  []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 1}},
		Arrivals: arrivals,
		Horizon:  msVT(500 * time.Millisecond),
		Seeds:    DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != n {
		t.Fatalf("expected all %d requests to eventually complete (queued work must never be lost), got %d", n, len(result.Completions))
	}
	for i := 1; i < len(result.Completions); i++ {
		if result.Completions[i].Latency < result.Completions[i-1].Latency {
			t.Errorf("completion %d latency %v is LESS than completion %d's %v -- expected monotonically non-decreasing latency under sustained overload",
				i, result.Completions[i].Latency, i-1, result.Completions[i-1].Latency)
		}
	}
}

// Determinism: the same overloaded Scenario run twice must produce
// byte-identical completion latencies and ordering -- capacity/queueing
// introduces no nondeterminism (no goroutines, no wall-clock, no map-
// iteration dependence).
func TestContention_Deterministic(t *testing.T) {
	arrivals := make([]Arrival, 8)
	for i := range arrivals {
		arrivals[i] = Arrival{At: msVT(time.Duration(i) * 3 * time.Millisecond), Key: "/x"}
	}
	scenario := Scenario{
		Targets:  []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 2}},
		Arrivals: arrivals,
		Horizon:  msVT(500 * time.Millisecond),
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
	if idx, diverged := FirstDivergence(a.Trace, b.Trace); diverged {
		t.Fatalf("two identical runs of a contention-enabled Scenario diverged at event %d", idx)
	}
	for i := range a.Completions {
		if a.Completions[i] != b.Completions[i] {
			t.Fatalf("completion %d differs between two identical runs: %+v vs %+v", i, a.Completions[i], b.Completions[i])
		}
	}
}

// No negative busy/load: a target's in-flight tracker must never be
// decremented below what it holds -- checked indirectly here via the
// public observable (no queued request is ever served twice, and every
// dispatched request completes exactly once), since targetState itself
// is private to RunWorld.
func TestContention_NoDoubleCompletionOrLoss(t *testing.T) {
	arrivals := make([]Arrival, 15)
	for i := range arrivals {
		arrivals[i] = Arrival{At: msVT(time.Duration(i) * 4 * time.Millisecond), Key: "/x"}
	}
	scenario := Scenario{
		Targets:  []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 2}},
		Arrivals: arrivals,
		Horizon:  msVT(1 * time.Second),
		Seeds:    DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != len(arrivals) {
		t.Fatalf("expected exactly %d completions (no duplicates, none lost), got %d", len(arrivals), len(result.Completions))
	}
	if result.CompletedByTarget["t1"] != len(arrivals) {
		t.Fatalf("CompletedByTarget mismatch: expected %d, got %d", len(arrivals), result.CompletedByTarget["t1"])
	}
}

// Failure interaction: a request already queued at a target does not
// vanish if that target fails while the request is still waiting --
// chosen semantic (docs/StageArtifacts/Stage12-Plan.md §5): queued work
// is unaffected by failure state, matching the existing precedent that
// FailureWindow only gates NEW routing eligibility, never already-
// committed work.
func TestContention_QueuedWorkSurvivesTargetFailure(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{{Name: "t1", ServiceTime: 10 * time.Millisecond, Capacity: 1}},
		Arrivals: []Arrival{
			{At: 0, Key: "/a"},                          // occupies the only slot until t=10ms
			{At: msVT(1 * time.Millisecond), Key: "/b"}, // queues behind /a
		},
		// t1 "fails" at t=2ms (while /b is already queued) and recovers
		// at t=5ms, well before /b would ever begin service (t=10ms).
		Failures:          []FailureWindow{{Target: "t1", DownAt: msVT(2 * time.Millisecond), UpAt: msVT(5 * time.Millisecond)}},
		UseHealthRegistry: true,
		Horizon:           msVT(100 * time.Millisecond),
		Seeds:             DeriveSeeds(1),
	}
	result, err := RunWorld(scenario, RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	if len(result.Completions) != 2 {
		t.Fatalf("expected both requests to eventually complete despite the intervening failure, got %d completions", len(result.Completions))
	}
	if result.Completions[1].Latency != 19*time.Millisecond {
		t.Errorf("expected /b's latency to be unaffected by t1's failure/recovery while queued (still 19ms, same as TestContention_OneSlot_SequentialWaiting), got %v", result.Completions[1].Latency)
	}
}

// TestContention_ScaleInvariance pins a genuine Stage 13 discovery
// (docs/StageArtifacts/Stage13.md, experiment-013d): scaling every
// target's ServiceTime and the Scenario's Horizon by the same factor k,
// while holding Requests fixed, keeps utilization (rho) exactly
// invariant by construction (arrival rate scales by 1/k, service time
// scales by k, rho = rate*serviceTime/capacity has no net k dependence)
// -- and every completion's Latency should scale by EXACTLY k too. This
// is a structural property of the deterministic queueing arithmetic
// itself (not an empirical claim about which policy wins), verified
// once with a hand-picked k so a future change to the queueing/timing
// code can't silently break scale invariance without a test noticing.
func TestContention_ScaleInvariance(t *testing.T) {
	build := func(k int) Scenario {
		arrivals := make([]Arrival, 5)
		for i := range arrivals {
			arrivals[i] = Arrival{At: msVT(time.Duration(i*k) * time.Millisecond), Key: "/x"}
		}
		return Scenario{
			Targets:  []TargetProfile{{Name: "t1", ServiceTime: time.Duration(10*k) * time.Millisecond, Capacity: 1}},
			Arrivals: arrivals,
			Horizon:  msVT(time.Duration(200*k) * time.Millisecond),
			Seeds:    DeriveSeeds(1),
		}
	}

	base, err := RunWorld(build(1), RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(k=1) failed: %v", err)
	}
	scaled, err := RunWorld(build(4), RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(k=4) failed: %v", err)
	}
	if len(base.Completions) != len(scaled.Completions) {
		t.Fatalf("expected the same number of completions at both scales, got %d vs %d", len(base.Completions), len(scaled.Completions))
	}
	for i := range base.Completions {
		want := base.Completions[i].Latency * 4
		got := scaled.Completions[i].Latency
		if got != want {
			t.Errorf("completion %d: expected latency to scale by exactly 4x (%v), got %v", i, want, got)
		}
	}
}
