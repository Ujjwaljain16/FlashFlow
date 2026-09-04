package proxy

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

func TestP2C_EmptyAvailable(t *testing.T) {
	sel := NewP2CSelector(ScorerFromLoad(NewLoadTracker()), rand.New(rand.NewSource(1)))
	_, err := sel.SelectTarget(nil, nil)
	if err != ErrNoHealthyTargets {
		t.Fatalf("expected ErrNoHealthyTargets, got %v", err)
	}
}

func TestP2C_SingleTarget(t *testing.T) {
	sel := NewP2CSelector(ScorerFromLoad(NewLoadTracker()), rand.New(rand.NewSource(1)))
	got, err := sel.SelectTarget(nil, []string{"only"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "only" {
		t.Fatalf("expected 'only', got %q", got)
	}
}

func TestP2C_TwoTargets_AlwaysComparesBoth(t *testing.T) {
	tr := NewLoadTracker()
	tr.Increment("a")
	tr.Increment("a") // a=2, b=0
	sel := NewP2CSelector(ScorerFromLoad(tr), rand.New(rand.NewSource(1)))

	// With exactly 2 candidates, every call samples both (there is no
	// third option), so the better one (b) must win every time regardless
	// of RNG draw order.
	for i := 0; i < 20; i++ {
		got, err := sel.SelectTarget(nil, []string{"a", "b"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "b" {
			t.Fatalf("call %d: expected 'b' (load 0) to always beat 'a' (load 2) when both are always compared, got %q", i, got)
		}
	}
}

// TestP2C_DeterministicUnderSeededRandomness proves selection behavior is
// reproducible: the exact same seed must produce the exact same sequence
// of selections given the same scorer state, which matters for
// reproducible experiments (see the Stage 3 hypotheses doc on controlled
// randomness).
func TestP2C_DeterministicUnderSeededRandomness(t *testing.T) {
	available := []string{"a", "b", "c", "d"}
	noopScorer := func(target string) (float64, bool) { return 0, true } // all tied; pure sampling determines outcome

	run := func() []string {
		sel := NewP2CSelector(noopScorer, rand.New(rand.NewSource(42)))
		var out []string
		for i := 0; i < 20; i++ {
			got, err := sel.SelectTarget(nil, available)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out = append(out, got)
		}
		return out
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("length mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("selection %d differed between two runs with the same seed: %q vs %q — randomness is not reproducible", i, first[i], second[i])
		}
	}
}

func TestPreferScore_Table(t *testing.T) {
	cases := []struct {
		name                   string
		score                  float64
		ok                     bool
		bestScore              float64
		bestOK                 bool
		wantCandidatePreferred bool
	}{
		{"unobserved beats observed", 0, false, 999, true, true},
		{"observed never beats unobserved", 999, true, 0, false, false},
		{"both unobserved: keep current best", 0, false, 0, false, false},
		{"both observed: lower wins", 5, true, 10, true, true},
		{"both observed: higher loses", 10, true, 5, true, false},
		{"both observed: exact tie keeps current best", 5, true, 5, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preferScore(tc.score, tc.ok, tc.bestScore, tc.bestOK)
			if got != tc.wantCandidatePreferred {
				t.Fatalf("preferScore(%v,%v,%v,%v) = %v, want %v", tc.score, tc.ok, tc.bestScore, tc.bestOK, got, tc.wantCandidatePreferred)
			}
		})
	}
}

func TestP2C_SamplePairReturnsDistinctInRangeIndices(t *testing.T) {
	sel := NewP2CSelector(nil, rand.New(rand.NewSource(7)))
	for n := 2; n <= 8; n++ {
		for trial := 0; trial < 200; trial++ {
			i, j := sel.samplePair(n)
			if i == j {
				t.Fatalf("n=%d: samplePair returned equal indices (%d, %d)", n, i, j)
			}
			if i < 0 || i >= n || j < 0 || j >= n {
				t.Fatalf("n=%d: samplePair returned out-of-range index (%d, %d)", n, i, j)
			}
		}
	}
}

// TestP2C_ExplorationDoesNotLockIn is the core property this selector
// exists to provide, tested directly rather than only inferred from an
// experiment: with many equally-scored targets, repeated selection must
// not collapse onto one of them the way EWMASelector's full-scan argmin
// did (Experiment 003-D). Every target should receive a roughly even
// share over enough selections.
func TestP2C_ExplorationDoesNotLockIn(t *testing.T) {
	available := []string{"a", "b", "c", "d", "e"}
	tiedScorer := func(target string) (float64, bool) { return 1.0, true } // all identical, always "observed"

	sel := NewP2CSelector(tiedScorer, rand.New(rand.NewSource(99)))
	counts := map[string]int{}
	const n = 5000
	for i := 0; i < n; i++ {
		got, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		counts[got]++
	}

	ideal := float64(n) / float64(len(available))
	for _, target := range available {
		share := float64(counts[target])
		// Generous tolerance (±30% of ideal) — this is a sanity check
		// against total lock-in (e.g. one target at 90%+, others at ~0%,
		// which is what EWMA showed), not a precision fairness test.
		if share < ideal*0.7 || share > ideal*1.3 {
			t.Fatalf("target %s received %v of %d selections (%.1f%%), expected roughly %.1f%% — "+
				"counts=%v suggest lock-in rather than exploration", target, counts[target], n, 100*share/float64(n), 100/float64(len(available)), counts)
		}
	}
}

// TestP2C_EqualRivalsBothStayFresh is the precise version of the
// exploration-fix claim: when two candidates are GENUINELY, deterministically
// tied, and only occasionally paired against each other directly (a third,
// worse candidate absorbs the rest of the comparisons), both tied
// candidates must still keep winning roughly half of their own head-to-head
// comparisons over many trials — neither should get frozen out the way
// EWMA's full-scan argmin froze one of two equal edges in Experiment 003-D.
func TestP2C_EqualRivalsBothStayFresh(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	tr.Observe("a", 1*time.Millisecond)
	tr.Observe("b", 1*time.Millisecond) // a and b are genuinely, permanently tied
	tr.Observe("c", 100*time.Millisecond)

	sel := NewP2CSelector(ScorerFromLatency(tr), rand.New(rand.NewSource(5)))
	available := []string{"a", "b", "c"}

	counts := map[string]int{}
	const n = 1500
	for i := 0; i < n; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		counts[target]++
		// Re-observing the winner at its own fixed latency keeps a/b tied
		// and c consistently worse, isolating the property under test.
		switch target {
		case "a":
			tr.Observe("a", 1*time.Millisecond)
		case "b":
			tr.Observe("b", 1*time.Millisecond)
		case "c":
			tr.Observe("c", 100*time.Millisecond)
		}
	}

	if counts["c"] != 0 {
		t.Fatalf("expected 'c' (deterministically worse) to never win a comparison, got %d wins", counts["c"])
	}
	// a and b should split roughly evenly between them (both receive the
	// remaining share after c's 0). A 30%-70% band is generous but would
	// still catch the kind of 94%/4% collapse EWMA showed.
	total := counts["a"] + counts["b"]
	aShare := float64(counts["a"]) / float64(total)
	if aShare < 0.30 || aShare > 0.70 {
		t.Fatalf("expected roughly even split between tied 'a' and 'b', got a=%.1f%% b=%.1f%% (counts=%v) — "+
			"this looks like the same lock-in EWMA showed, not a fix for it", aShare*100, 100-aShare*100, counts)
	}
}

// TestP2C_LatencyBased_CannotDetectRecoveryOfLosingTarget documents a real,
// verified LIMITATION discovered while testing the claim above: P2C only
// records an observation for the target it actually dispatches to (the
// winner of a comparison). A target that is genuinely, deterministically
// worse than its rivals therefore never wins, is never dispatched, and is
// never re-observed — so if its true latency later improves, a
// latency-scored P2C selector has no way to find out, same as EWMA's
// full-scan argmin. See the P2CSelector doc comment for the precise
// boundary of what P2C does and does not fix, and
// TestP2C_LoadBased_RecoveryIsAlwaysDetectable below for the scorer that
// does not share this problem.
func TestP2C_LatencyBased_CannotDetectRecoveryOfLosingTarget(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	tr.Observe("a", 1*time.Millisecond)
	tr.Observe("b", 1*time.Millisecond)
	tr.Observe("c", 100*time.Millisecond) // c starts clearly, deterministically worse

	sel := NewP2CSelector(ScorerFromLatency(tr), rand.New(rand.NewSource(5)))
	available := []string{"a", "b", "c"}

	for i := 0; i < 300; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Only the dispatched (winning) target is re-observed, exactly as
		// ReverseProxy.ServeHTTP would only call LatencyTracker.Observe
		// for the target it actually sent the request to.
		switch target {
		case "a":
			tr.Observe("a", 1*time.Millisecond)
		case "b":
			tr.Observe("b", 1*time.Millisecond)
		case "c":
			tr.Observe("c", 100*time.Millisecond)
		}
	}

	// c's TRUE latency now improves dramatically, but nothing tells the
	// tracker this, because nothing has been dispatching to c to find out.
	trueCEstimateBeforePretendRecovery, _ := tr.Estimate("c")

	seenCAfterPretendRecovery := false
	for i := 0; i < 200; i++ {
		target, _ := sel.SelectTarget(nil, available)
		if target == "c" {
			seenCAfterPretendRecovery = true
			break
		}
	}

	if seenCAfterPretendRecovery {
		t.Fatalf("expected 'c' to remain unselected even after its (unmodeled) true latency improved — " +
			"if this now passes, the limitation documented on P2CSelector no longer holds and that comment must be updated")
	}
	stillStaleEstimate, _ := tr.Estimate("c")
	if stillStaleEstimate != trueCEstimateBeforePretendRecovery {
		t.Fatalf("expected c's estimate to remain frozen at %v with no dispatches to refresh it, got %v",
			trueCEstimateBeforePretendRecovery, stillStaleEstimate)
	}
}

// TestP2C_LoadBased_RecoveryIsAlwaysDetectable shows the load-scored case
// does NOT share the limitation above: an idle target's in-flight count is
// always exactly 0 — live state, not a memory of a past sample — so it is
// never treated as "worse than it currently is" just because it hasn't
// been picked recently.
func TestP2C_LoadBased_RecoveryIsAlwaysDetectable(t *testing.T) {
	tr := NewLoadTracker()
	sel := NewP2CSelector(ScorerFromLoad(tr), rand.New(rand.NewSource(5)))
	available := []string{"a", "b"}

	// Keep "a" busy for a long stretch; "b" is never touched.
	for i := 0; i < 5; i++ {
		tr.Increment("a")
	}
	for i := 0; i < 200; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != "b" {
			t.Fatalf("call %d: expected idle 'b' (load 0) to always beat busy 'a' (load 5), got %q", i, target)
		}
	}

	// "a" finishes its work; its load returns to 0 (ground truth, not a
	// stale memory) and it is immediately, correctly eligible again.
	for i := 0; i < 5; i++ {
		tr.Decrement("a")
	}
	seenA := false
	for i := 0; i < 20; i++ {
		target, _ := sel.SelectTarget(nil, available)
		if target == "a" {
			seenA = true
			break
		}
	}
	if !seenA {
		t.Fatalf("expected 'a' to be selectable again immediately once its load returned to 0, got 0 selections across 20 tries")
	}
}

func TestP2C_ConcurrentSelection(t *testing.T) {
	tr := NewLoadTracker()
	sel := NewP2CSelector(ScorerFromLoad(tr), rand.New(rand.NewSource(3)))
	available := []string{"a", "b", "c"}
	valid := map[string]bool{"a": true, "b": true, "c": true}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			target, err := sel.SelectTarget(nil, available)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if !valid[target] {
				t.Errorf("SelectTarget returned %q, not one of the candidates %v", target, available)
				return
			}
			tr.Increment(target)
			tr.Decrement(target)
		}()
	}
	wg.Wait()
}
