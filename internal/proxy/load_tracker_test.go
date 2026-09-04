package proxy

import (
	"sync"
	"testing"
)

func TestLoadTracker_IncrementDecrement(t *testing.T) {
	tr := NewLoadTracker()
	if got := tr.Get("a"); got != 0 {
		t.Fatalf("expected 0 for unseen target, got %d", got)
	}
	tr.Increment("a")
	tr.Increment("a")
	if got := tr.Get("a"); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	tr.Decrement("a")
	if got := tr.Get("a"); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestLoadTracker_DecrementFlooredAtZero(t *testing.T) {
	tr := NewLoadTracker()
	tr.Decrement("a") // never incremented
	tr.Decrement("a")
	if got := tr.Get("a"); got != 0 {
		t.Fatalf("expected floor at 0 for never-incremented target, got %d", got)
	}

	tr.Increment("a")
	tr.Decrement("a")
	tr.Decrement("a") // one extra decrement beyond the increment
	if got := tr.Get("a"); got != 0 {
		t.Fatalf("expected floor at 0 after over-decrementing, got %d", got)
	}
}

func TestLoadTracker_SnapshotIsIndependentCopy(t *testing.T) {
	tr := NewLoadTracker()
	tr.Increment("a")
	tr.Increment("a")
	tr.Increment("b")

	snap := tr.Snapshot()
	if snap["a"] != 2 || snap["b"] != 1 {
		t.Fatalf("unexpected snapshot: %v", snap)
	}

	snap["a"] = 999
	if tr.Get("a") != 2 {
		t.Fatalf("Snapshot must return an independent copy; internal state was mutated via the returned map")
	}
}

// TestLoadTracker_ConcurrentIncrementDecrement is the concurrency-safety
// requirement for stateful Stage 3 routers: equal increments and
// decrements from many goroutines must net to exactly zero, with no lost
// or duplicated update.
func TestLoadTracker_ConcurrentIncrementDecrement(t *testing.T) {
	tr := NewLoadTracker()
	const goroutines = 100
	const opsPerGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				tr.Increment("shared")
			}
			for i := 0; i < opsPerGoroutine; i++ {
				tr.Decrement("shared")
			}
		}()
	}
	wg.Wait()

	if got := tr.Get("shared"); got != 0 {
		t.Fatalf("expected net 0 after %d goroutines each doing %d increments and %d decrements, got %d",
			goroutines, opsPerGoroutine, opsPerGoroutine, got)
	}
}
