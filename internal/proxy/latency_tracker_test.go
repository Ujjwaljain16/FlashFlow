package proxy

import (
	"sync"
	"testing"
	"time"
)

func TestLatencyTracker_FirstObservationSeedsDirectly(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	if _, ok := tr.Estimate("a"); ok {
		t.Fatalf("expected no estimate before any observation")
	}
	tr.Observe("a", 100*time.Millisecond)
	got, ok := tr.Estimate("a")
	if !ok {
		t.Fatalf("expected ok=true after first observation")
	}
	if got != 100*time.Millisecond {
		t.Fatalf("expected first observation to seed the estimate directly (100ms), got %v", got)
	}
}

// TestLatencyTracker_SmoothingFormula verifies the exact EWMA update rule:
// estimate = alpha*sample + (1-alpha)*estimate. This is a precise
// numerical check, not an approximate one, because EWMASelector's cold-
// start and preference logic both depend on the formula being exactly
// what it claims to be.
func TestLatencyTracker_SmoothingFormula(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	tr.Observe("a", 100*time.Millisecond) // seeds at 100ms
	tr.Observe("a", 200*time.Millisecond) // 0.5*200 + 0.5*100 = 150ms

	got, _ := tr.Estimate("a")
	want := 150 * time.Millisecond
	if got != want {
		t.Fatalf("expected %v after alpha=0.5 blend of 100ms and 200ms, got %v", want, got)
	}

	tr.Observe("a", 200*time.Millisecond) // 0.5*200 + 0.5*150 = 175ms
	got, _ = tr.Estimate("a")
	want = 175 * time.Millisecond
	if got != want {
		t.Fatalf("expected %v after third observation, got %v", want, got)
	}
}

// TestLatencyTracker_OneOutlierDoesNotDominate is the "does one slow
// request dominate?" question from the Stage 3 brief, answered
// numerically: a single large outlier shifts the estimate by exactly
// alpha's fraction of the gap, not to the outlier's full value.
func TestLatencyTracker_OneOutlierDoesNotDominate(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	// Establish a stable baseline via repeated identical observations.
	for i := 0; i < 20; i++ {
		tr.Observe("a", 10*time.Millisecond)
	}
	baseline, _ := tr.Estimate("a")
	if baseline != 10*time.Millisecond {
		t.Fatalf("expected stable baseline of 10ms, got %v", baseline)
	}

	// One catastrophic outlier (1 full second).
	tr.Observe("a", 1000*time.Millisecond)
	got, _ := tr.Estimate("a")
	want := time.Duration(0.2*float64(1000*time.Millisecond) + 0.8*float64(10*time.Millisecond))
	if got != want {
		t.Fatalf("expected exactly alpha-weighted shift to %v, got %v", want, got)
	}
	if got >= 300*time.Millisecond {
		t.Fatalf("a single outlier must not dominate the estimate: got %v, which is far closer to the 1000ms "+
			"outlier than the alpha=0.2 formula allows", got)
	}
}

func TestLatencyTracker_InvalidAlphaCoercedToDefault(t *testing.T) {
	for _, badAlpha := range []float64{0, -1, 1.5} {
		tr := NewLatencyTracker(badAlpha)
		tr.Observe("a", 100*time.Millisecond)
		tr.Observe("a", 200*time.Millisecond)
		got, _ := tr.Estimate("a")
		want := time.Duration(defaultEWMAAlpha*float64(200*time.Millisecond) + (1-defaultEWMAAlpha)*float64(100*time.Millisecond))
		if got != want {
			t.Fatalf("alpha=%v: expected coercion to defaultEWMAAlpha (%.2f) giving %v, got %v",
				badAlpha, defaultEWMAAlpha, want, got)
		}
	}
}

func TestLatencyTracker_TargetsAreIndependent(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	tr.Observe("a", 10*time.Millisecond)
	tr.Observe("b", 500*time.Millisecond)

	a, _ := tr.Estimate("a")
	b, _ := tr.Estimate("b")
	if a != 10*time.Millisecond || b != 500*time.Millisecond {
		t.Fatalf("expected independent per-target estimates, got a=%v b=%v", a, b)
	}
}

func TestLatencyTracker_SnapshotIsIndependentCopy(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	tr.Observe("a", 10*time.Millisecond)

	snap := tr.Snapshot()
	snap["a"] = 999 * time.Second
	got, _ := tr.Estimate("a")
	if got != 10*time.Millisecond {
		t.Fatalf("Snapshot must return an independent copy; internal state was mutated via the returned map")
	}
}

func TestLatencyTracker_ConcurrentObserve(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tr.Observe("shared", 10*time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// All observations are identical (10ms), so regardless of goroutine
	// interleaving order, the estimate must converge to exactly 10ms —
	// this is a concurrency-safety check disguised as a numerical one: if
	// updates were lost or corrupted under concurrent access, the result
	// would drift from 10ms.
	got, ok := tr.Estimate("shared")
	if !ok || got != 10*time.Millisecond {
		t.Fatalf("expected exactly 10ms after %d identical concurrent observations, got %v (ok=%t)",
			goroutines*perGoroutine, got, ok)
	}
}
