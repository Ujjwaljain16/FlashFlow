package proxy

import (
	"sync"
	"testing"
	"time"
)

// TestEWMA_UnobservedBeatsObserved is the cold-start rule this selector
// exists to test: even an excellent real latency estimate must lose to a
// target that has never been tried, so the selector is forced to explore
// every target at least once before trusting any of its own history.
func TestEWMA_UnobservedBeatsObserved(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	tr.Observe("a", 1*time.Millisecond) // excellent latency
	// "b" has never been observed.

	sel := NewEWMASelector(tr)
	got, err := sel.SelectTarget(nil, []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b" {
		t.Fatalf("expected unobserved target 'b' to beat observed-but-excellent 'a', got %q", got)
	}
}

func TestEWMA_AllUnobservedTieBreaksToFirstInOrder(t *testing.T) {
	sel := NewEWMASelector(NewLatencyTracker(0.2))
	got, err := sel.SelectTarget(nil, []string{"b", "a", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b" {
		t.Fatalf("expected tie-break to first in `available` order ('b'), got %q", got)
	}
}

func TestEWMA_LowerLatencyWinsOnceAllObserved(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	tr.Observe("a", 50*time.Millisecond)
	tr.Observe("b", 10*time.Millisecond)
	tr.Observe("c", 100*time.Millisecond)

	sel := NewEWMASelector(tr)
	got, err := sel.SelectTarget(nil, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b" {
		t.Fatalf("expected 'b' (lowest observed latency, 10ms), got %q", got)
	}
}

func TestEWMA_ReactsToChangingLatency(t *testing.T) {
	tr := NewLatencyTracker(0.5)
	sel := NewEWMASelector(tr)
	available := []string{"a", "b"}

	tr.Observe("a", 10*time.Millisecond)
	tr.Observe("b", 10*time.Millisecond)
	got, _ := sel.SelectTarget(nil, available)
	if got != "a" {
		t.Fatalf("expected 'a' on an exact tie (first in order), got %q", got)
	}

	// "a" degrades sharply.
	tr.Observe("a", 500*time.Millisecond)
	got, _ = sel.SelectTarget(nil, available)
	if got != "b" {
		t.Fatalf("expected 'b' once 'a' degrades, got %q", got)
	}
}

func TestEWMA_EmptyAvailable(t *testing.T) {
	sel := NewEWMASelector(NewLatencyTracker(0.2))
	_, err := sel.SelectTarget(nil, nil)
	if err != ErrNoHealthyTargets {
		t.Fatalf("expected ErrNoHealthyTargets, got %v", err)
	}
}

func TestEWMA_SingleTarget(t *testing.T) {
	sel := NewEWMASelector(NewLatencyTracker(0.2))
	got, err := sel.SelectTarget(nil, []string{"only"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "only" {
		t.Fatalf("expected 'only', got %q", got)
	}
}

// TestEWMA_ConcurrentSelection is a concurrency-safety smoke test: many
// goroutines selecting and observing concurrently must not panic, and
// every call must return one of the valid candidates. It deliberately does
// NOT assert that every target gets observed at least once — that is not
// actually guaranteed: SelectTarget's read of LatencyTracker.Estimate and
// the caller's later Observe are two separate steps (the same
// read-then-act pattern documented on LoadTracker.Get), so a burst of
// goroutines can race through several "all unobserved" selections before
// any Observe lands, all picking the same tie-break winner. Asserting
// full coverage here would be an unverified claim dressed up as a test.
func TestEWMA_ConcurrentSelection(t *testing.T) {
	tr := NewLatencyTracker(0.2)
	sel := NewEWMASelector(tr)
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
			tr.Observe(target, 5*time.Millisecond)
		}()
	}
	wg.Wait()
}
