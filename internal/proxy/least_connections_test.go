package proxy

import (
	"sync"
	"testing"
)

func TestLeastConnections_SelectsLowestLoad(t *testing.T) {
	tr := NewLoadTracker()
	tr.Increment("a")
	tr.Increment("a")
	tr.Increment("b")
	// a=2, b=1, c=0

	sel := NewLeastConnectionsSelector(tr)
	got, err := sel.SelectTarget(nil, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "c" {
		t.Fatalf("expected 'c' (load 0), got %q", got)
	}
}

func TestLeastConnections_TieBreaksToFirstInOrder(t *testing.T) {
	tr := NewLoadTracker() // all targets at load 0
	sel := NewLeastConnectionsSelector(tr)
	got, err := sel.SelectTarget(nil, []string{"b", "a", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b" {
		t.Fatalf("expected tie-break to first in `available` order ('b'), got %q", got)
	}
}

func TestLeastConnections_ReactsToChangingLoad(t *testing.T) {
	tr := NewLoadTracker()
	sel := NewLeastConnectionsSelector(tr)
	available := []string{"a", "b"}

	got, err := sel.SelectTarget(nil, available)
	if err != nil || got != "a" {
		t.Fatalf("expected 'a' initially (tie, first in order), got %q err=%v", got, err)
	}

	// Simulate "a" accumulating in-flight load.
	tr.Increment("a")
	tr.Increment("a")
	tr.Increment("a")

	got, err = sel.SelectTarget(nil, available)
	if err != nil || got != "b" {
		t.Fatalf("expected 'b' once 'a' is loaded, got %q err=%v", got, err)
	}

	// Simulate those requests completing.
	tr.Decrement("a")
	tr.Decrement("a")
	tr.Decrement("a")

	got, err = sel.SelectTarget(nil, available)
	if err != nil || got != "a" {
		t.Fatalf("expected 'a' again once load returns to the tie state, got %q err=%v", got, err)
	}
}

func TestLeastConnections_EmptyAvailable(t *testing.T) {
	sel := NewLeastConnectionsSelector(NewLoadTracker())
	_, err := sel.SelectTarget(nil, nil)
	if err != ErrNoHealthyTargets {
		t.Fatalf("expected ErrNoHealthyTargets, got %v", err)
	}
}

func TestLeastConnections_SingleTarget(t *testing.T) {
	sel := NewLeastConnectionsSelector(NewLoadTracker())
	got, err := sel.SelectTarget(nil, []string{"only"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "only" {
		t.Fatalf("expected 'only', got %q", got)
	}
}

// TestLeastConnections_ConcurrentSelection is a concurrency-safety smoke
// test, not a fairness test: LeastConnectionsSelector's read-then-increment
// pattern (selection reads LoadTracker.Get; the proxy's Increment happens
// as a separate later step) has a documented, bounded race under
// simultaneous concurrent calls — see LoadTracker.Get's doc comment. This
// test only asserts that concurrent use does not panic or corrupt the
// tracker, not that load stays perfectly balanced under a burst.
func TestLeastConnections_ConcurrentSelection(t *testing.T) {
	tr := NewLoadTracker()
	sel := NewLeastConnectionsSelector(tr)
	available := []string{"a", "b", "c"}

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
			tr.Increment(target)
			tr.Decrement(target)
		}()
	}
	wg.Wait()

	for _, target := range available {
		if got := tr.Get(target); got != 0 {
			t.Fatalf("expected all targets back at load 0 after equal increment/decrement pairs, target %s has %d", target, got)
		}
	}
}
