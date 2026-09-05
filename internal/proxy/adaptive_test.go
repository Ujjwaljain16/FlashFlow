package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flashflow/internal/clock"
)

func req(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

func TestAdaptiveSelector_NoAvailableTargetsReturnsError(t *testing.T) {
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())
	if _, err := sel.SelectTarget(req("/a"), nil); err != ErrNoHealthyTargets {
		t.Fatalf("expected ErrNoHealthyTargets, got %v", err)
	}
}

func TestAdaptiveSelector_SingleTargetAlwaysWins(t *testing.T) {
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())
	target, err := sel.SelectTarget(req("/a"), []string{"only"})
	if err != nil || target != "only" {
		t.Fatalf("expected \"only\", got %q err=%v", target, err)
	}
}

// TestAdaptiveSelector_LowerLoadWins is the core monotonicity check for
// the Load signal: increasing load must not make a target MORE
// desirable.
func TestAdaptiveSelector_LowerLoadWins(t *testing.T) {
	lt := NewLoadTracker()
	lt.Increment("busy")
	lt.Increment("busy")
	lt.Increment("busy")
	// "idle" has zero load.

	sel := NewAdaptiveSelector(lt, NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())
	target, err := sel.SelectTarget(req("/a"), []string{"busy", "idle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "idle" {
		t.Fatalf("expected the less-loaded target to win, got %q", target)
	}
}

// TestAdaptiveSelector_UtilizationAccountsForCapacity checks that Load
// is judged relative to configured Capacity, not in absolute terms --
// the direct synthesis of Stage 3's WRR-vs-load-signal lesson.
func TestAdaptiveSelector_UtilizationAccountsForCapacity(t *testing.T) {
	lt := NewLoadTracker()
	for i := 0; i < 10; i++ {
		lt.Increment("big") // higher absolute load...
	}
	for i := 0; i < 5; i++ {
		lt.Increment("small") // ...but "small" is proportionally busier
	}
	capacity := TargetWeights{"big": 20, "small": 5} // big: 10/20=50% util, small: 5/5=100% util

	sel := NewAdaptiveSelector(lt, NewLatencyTracker(0.2), capacity, nil, nil, DefaultAdaptiveConfig())
	target, err := sel.SelectTarget(req("/a"), []string{"big", "small"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "big" {
		t.Fatalf("expected \"big\" to win on lower utilization despite higher absolute load, got %q", target)
	}
}

// TestAdaptiveSelector_LowerLatencyWins is the core monotonicity check
// for the Latency signal: increasing latency must not improve a
// target's score.
func TestAdaptiveSelector_LowerLatencyWins(t *testing.T) {
	lat := NewLatencyTracker(0.2)
	lat.Observe("fast", 5*time.Millisecond)
	lat.Observe("slow", 500*time.Millisecond)

	mc := clock.NewMockClock(1000)
	sel := NewAdaptiveSelector(NewLoadTracker(), lat, nil, nil, mc, DefaultAdaptiveConfig())
	// Mark both as freshly selected (relative to the same mock clock) so
	// neither falls back to neutral due to staleness.
	sel.lastSelected["fast"] = 1000
	sel.lastSelected["slow"] = 1000

	target, err := sel.SelectTarget(req("/a"), []string{"fast", "slow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "fast" {
		t.Fatalf("expected the lower-latency target to win, got %q", target)
	}
}

// TestAdaptiveSelector_UnobservedLatencyIsNeutralNotOptimistic is the
// key design differentiator from EWMASelector: an unobserved target
// must NOT automatically beat an observed target with good latency.
// Experiment 006-B proved EWMA's opposite rule ("unobserved beats
// observed") causes deterministic winner-take-all lock-in among equal
// targets; Adaptive is deliberately built not to have that failure mode.
func TestAdaptiveSelector_UnobservedLatencyIsNeutralNotOptimistic(t *testing.T) {
	lat := NewLatencyTracker(0.2)
	lat.Observe("proven-fast", 1*time.Millisecond) // excellent, well-observed latency
	// "never-tried" has no observation at all.

	mc := clock.NewMockClock(1000)
	sel := NewAdaptiveSelector(NewLoadTracker(), lat, nil, nil, mc, DefaultAdaptiveConfig())
	sel.lastSelected["proven-fast"] = 1000

	target, err := sel.SelectTarget(req("/a"), []string{"proven-fast", "never-tried"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "proven-fast" {
		t.Fatalf("expected the proven-fast target to win over an unobserved one (neutral cold start, not optimistic), got %q", target)
	}
}

// TestAdaptiveSelector_StaleLatencyFallsBackToNeutral proves the fix for
// Stage 3/6's demonstrated staleness blind spot: an old observation
// (this target hasn't been selected recently enough to trust its
// latency estimate) must not be trusted as if it were current.
func TestAdaptiveSelector_StaleLatencyFallsBackToNeutral(t *testing.T) {
	lat := NewLatencyTracker(0.2)
	lat.Observe("stale-bad", 900*time.Millisecond) // observed as very slow, long ago
	lat.Observe("fresh-mediocre", 150*time.Millisecond)

	cfg := DefaultAdaptiveConfig()
	cfg.StaleAfter = 100 * time.Millisecond

	sel := NewAdaptiveSelector(NewLoadTracker(), lat, nil, nil, nil, cfg)
	sel.lastSelected["stale-bad"] = 0                                             // selected long ago
	sel.lastSelected["fresh-mediocre"] = clock.VirtualTime(50 * time.Millisecond) // selected recently

	// "now" = 200ms: stale-bad's data is 200ms old (> 100ms threshold,
	// falls back to neutral 0.5); fresh-mediocre's is 150ms old (also
	// stale by this clock -- adjust so only one is actually stale).
	mc := clock.NewMockClock(clock.VirtualTime(150 * time.Millisecond))
	sel.clock = mc

	scores := sel.Explain(req("/a"), []string{"stale-bad", "fresh-mediocre"})
	byTarget := map[string]TargetScore{}
	for _, s := range scores {
		byTarget[s.Target] = s
	}

	// stale-bad: now(150ms) - lastSelected(0) = 150ms > 100ms threshold -> neutral 0.5
	if got := byTarget["stale-bad"].LatencyScore; got != 0.5 {
		t.Fatalf("expected stale-bad's latency score to fall back to neutral 0.5, got %v", got)
	}
	// fresh-mediocre: now(150ms) - lastSelected(50ms) = 100ms, NOT > 100ms threshold -> trusted
	if got := byTarget["fresh-mediocre"].LatencyScore; got == 0.5 {
		t.Fatalf("expected fresh-mediocre's latency score to be trusted (not exactly neutral), got %v", got)
	}
}

// TestAdaptiveSelector_CacheAffinityPrefersLastServer checks the Cache
// signal directly: all else equal, the target that last served this
// exact key should win.
func TestAdaptiveSelector_CacheAffinityPrefersLastServer(t *testing.T) {
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())

	// First request to /hot-key establishes affinity for whichever
	// target wins (both are identical, so the deterministic tie-break --
	// alphabetically first -- picks "edge-a").
	first, err := sel.SelectTarget(req("/hot-key"), []string{"edge-a", "edge-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != "edge-a" {
		t.Fatalf("expected the deterministic tie-break to pick edge-a first, got %q", first)
	}

	// A second request for the SAME key should now prefer edge-a again,
	// specifically because of cache affinity (all other signals are
	// still identical between the two targets).
	second, err := sel.SelectTarget(req("/hot-key"), []string{"edge-a", "edge-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second != "edge-a" {
		t.Fatalf("expected cache affinity to keep routing /hot-key to edge-a, got %q", second)
	}
}

// TestAdaptiveSelector_CostPenalizesExpensiveTarget checks the Cost
// signal: all else equal, the cheaper target should win.
func TestAdaptiveSelector_CostPenalizesExpensiveTarget(t *testing.T) {
	cost := TargetWeights{"cheap": 1, "expensive": 10}
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, cost, nil, DefaultAdaptiveConfig())

	target, err := sel.SelectTarget(req("/a"), []string{"cheap", "expensive"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "cheap" {
		t.Fatalf("expected the cheaper target to win, got %q", target)
	}
}

// TestAdaptiveSelector_DeterministicTieBreak proves ties resolve the
// same way regardless of input order -- critical for counterfactual
// replay, where map/slice ordering must never be a source of divergence.
func TestAdaptiveSelector_DeterministicTieBreak(t *testing.T) {
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())

	for _, order := range [][]string{
		{"zebra", "alpha", "mango"},
		{"mango", "zebra", "alpha"},
		{"alpha", "mango", "zebra"},
	} {
		target, err := sel.SelectTarget(req("/fresh-key-each-time-not-needed"), order)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != "alpha" {
			t.Fatalf("expected the alphabetically-first target to win every tie regardless of input order %v, got %q", order, target)
		}
	}
}

func TestAdaptiveSelector_ExplainDoesNotMutateState(t *testing.T) {
	sel := NewAdaptiveSelector(NewLoadTracker(), NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())

	_ = sel.Explain(req("/a"), []string{"x", "y"})
	if len(sel.lastSelected) != 0 {
		t.Fatalf("expected Explain to leave lastSelected untouched, got %v", sel.lastSelected)
	}
	if len(sel.keyAffinity) != 0 {
		t.Fatalf("expected Explain to leave keyAffinity untouched, got %v", sel.keyAffinity)
	}
}

func TestAdaptiveSelector_ExplainMatchesSelectTarget(t *testing.T) {
	lt := NewLoadTracker()
	lt.Increment("busy")
	sel := NewAdaptiveSelector(lt, NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())

	scores := sel.Explain(req("/a"), []string{"busy", "idle"})
	bestByExplain, bestScore := "", -1.0
	for _, s := range scores {
		if s.CombinedScore > bestScore {
			bestScore = s.CombinedScore
			bestByExplain = s.Target
		}
	}

	winner, err := sel.SelectTarget(req("/a"), []string{"busy", "idle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if winner != bestByExplain {
		t.Fatalf("expected SelectTarget's winner (%q) to match Explain's top score (%q)", winner, bestByExplain)
	}
}

func TestAdaptiveSelector_ConcurrentSelection(t *testing.T) {
	lt := NewLoadTracker()
	sel := NewAdaptiveSelector(lt, NewLatencyTracker(0.2), nil, nil, nil, DefaultAdaptiveConfig())

	targets := []string{"a", "b", "c"}
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				if _, err := sel.SelectTarget(req("/a"), targets); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
