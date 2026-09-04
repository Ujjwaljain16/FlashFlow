package vtime

import (
	"math/rand"
	"sort"
	"testing"

	"flashflow/internal/clock"
)

func TestEventQueue_EmptyQueue(t *testing.T) {
	q := NewEventQueue()
	if q.Len() != 0 {
		t.Fatalf("expected Len 0, got %d", q.Len())
	}
	if _, ok := q.Peek(); ok {
		t.Fatal("expected Peek on an empty queue to report false")
	}
	if _, _, ok := q.Pop(); ok {
		t.Fatal("expected Pop on an empty queue to report false")
	}
}

func TestEventQueue_PopsInTimestampOrder(t *testing.T) {
	q := NewEventQueue()
	var order []int
	q.Schedule(30, func() { order = append(order, 30) })
	q.Schedule(10, func() { order = append(order, 10) })
	q.Schedule(20, func() { order = append(order, 20) })

	for {
		_, cb, ok := q.Pop()
		if !ok {
			break
		}
		cb()
	}

	want := []int{10, 20, 30}
	if len(order) != len(want) {
		t.Fatalf("expected %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, order)
		}
	}
}

// TestEventQueue_SameTimestampOrderedByInsertionSequence is the
// scientifically important case: three events at the identical virtual
// time must always execute in the order they were scheduled, run
// repeatedly to rule out the heap's internal tie-breaking happening to
// look ordered by accident.
func TestEventQueue_SameTimestampOrderedByInsertionSequence(t *testing.T) {
	for trial := 0; trial < 20; trial++ {
		q := NewEventQueue()
		var order []string
		q.Schedule(100, func() { order = append(order, "A") })
		q.Schedule(100, func() { order = append(order, "B") })
		q.Schedule(100, func() { order = append(order, "C") })

		for {
			_, cb, ok := q.Pop()
			if !ok {
				break
			}
			cb()
		}

		want := []string{"A", "B", "C"}
		for i := range want {
			if order[i] != want[i] {
				t.Fatalf("trial %d: expected %v, got %v", trial, want, order)
			}
		}
	}
}

func TestEventQueue_InterleavedTimestampsAndSequence(t *testing.T) {
	q := NewEventQueue()
	var order []string
	// Two events at t=10 (X before Y), one at t=5, one at t=10 again (Z),
	// scheduled out of timestamp order to make sure Schedule order never
	// leaks into the result except as the same-timestamp tie-breaker.
	q.Schedule(10, func() { order = append(order, "X") })
	q.Schedule(5, func() { order = append(order, "early") })
	q.Schedule(10, func() { order = append(order, "Y") })
	q.Schedule(10, func() { order = append(order, "Z") })

	for {
		_, cb, ok := q.Pop()
		if !ok {
			break
		}
		cb()
	}

	want := []string{"early", "X", "Y", "Z"}
	if len(order) != len(want) {
		t.Fatalf("expected %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, order)
		}
	}
}

func TestEventQueue_CancelPreventsExecution(t *testing.T) {
	q := NewEventQueue()
	ran := false
	id := q.Schedule(10, func() { ran = true })

	if ok := q.Cancel(id); !ok {
		t.Fatal("expected Cancel to succeed on a pending event")
	}
	if q.Len() != 0 {
		t.Fatalf("expected Len 0 after cancelling the only event, got %d", q.Len())
	}
	if _, _, ok := q.Pop(); ok {
		t.Fatal("expected the cancelled event to never be popped")
	}
	if ran {
		t.Fatal("expected the cancelled callback to never run")
	}
}

// TestEventQueue_CancelBuriedEntryIsSkippedOnPop cancels an event that
// sits deep in the heap (not at the root) and proves it's still skipped
// once it would otherwise bubble to the front — exercising the lazy
// removal path, not just the trivial single-event case.
func TestEventQueue_CancelBuriedEntryIsSkippedOnPop(t *testing.T) {
	q := NewEventQueue()
	var order []int

	farFuture := q.Schedule(1000, func() { order = append(order, 1000) })
	q.Schedule(10, func() { order = append(order, 10) })
	q.Schedule(20, func() { order = append(order, 20) })
	q.Schedule(30, func() { order = append(order, 30) })

	if ok := q.Cancel(farFuture); !ok {
		t.Fatal("expected Cancel to succeed")
	}

	for {
		_, cb, ok := q.Pop()
		if !ok {
			break
		}
		cb()
	}

	want := []int{10, 20, 30}
	if len(order) != len(want) {
		t.Fatalf("expected %v (1000 cancelled), got %v", want, order)
	}
}

func TestEventQueue_CancelUnknownIDReturnsFalse(t *testing.T) {
	q := NewEventQueue()
	if ok := q.Cancel(EventID(9999)); ok {
		t.Fatal("expected Cancel on an unknown id to return false")
	}
}

func TestEventQueue_CancelAlreadyPoppedReturnsFalse(t *testing.T) {
	q := NewEventQueue()
	id := q.Schedule(10, func() {})
	if _, _, ok := q.Pop(); !ok {
		t.Fatal("expected Pop to succeed")
	}
	if ok := q.Cancel(id); ok {
		t.Fatal("expected Cancel on an already-popped id to return false")
	}
}

func TestEventQueue_CancelTwiceReturnsFalseSecondTime(t *testing.T) {
	q := NewEventQueue()
	id := q.Schedule(10, func() {})
	if ok := q.Cancel(id); !ok {
		t.Fatal("expected the first Cancel to succeed")
	}
	if ok := q.Cancel(id); ok {
		t.Fatal("expected the second Cancel on the same id to return false")
	}
}

func TestEventQueue_PeekDoesNotRemove(t *testing.T) {
	q := NewEventQueue()
	q.Schedule(10, func() {})
	q.Schedule(20, func() {})

	at1, ok := q.Peek()
	if !ok || at1 != 10 {
		t.Fatalf("expected Peek to report 10, got %d ok=%v", at1, ok)
	}
	at2, ok := q.Peek()
	if !ok || at2 != 10 {
		t.Fatalf("expected a second Peek to still report 10, got %d ok=%v", at2, ok)
	}
	if q.Len() != 2 {
		t.Fatalf("expected Peek to leave both events pending, got Len=%d", q.Len())
	}

	poppedAt, _, ok := q.Pop()
	if !ok || poppedAt != 10 {
		t.Fatalf("expected Pop to return the same event Peek reported, got %d ok=%v", poppedAt, ok)
	}
}

func TestEventQueue_LenReflectsCancellation(t *testing.T) {
	q := NewEventQueue()
	q.Schedule(10, func() {})
	id2 := q.Schedule(20, func() {})
	q.Schedule(30, func() {})

	if q.Len() != 3 {
		t.Fatalf("expected Len 3, got %d", q.Len())
	}
	q.Cancel(id2)
	if q.Len() != 2 {
		t.Fatalf("expected Len 2 after cancelling one, got %d", q.Len())
	}
}

// TestEventQueue_PopOrderMatchesSortedTimestamps is a property-style
// check: for many randomly-timed events, Pop must return them in
// nondecreasing timestamp order, every time.
func TestEventQueue_PopOrderMatchesSortedTimestamps(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 10; trial++ {
		q := NewEventQueue()
		const n = 200
		times := make([]clock.VirtualTime, n)
		for i := 0; i < n; i++ {
			at := clock.VirtualTime(r.Int63n(10000))
			times[i] = at
			q.Schedule(at, func() {})
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

		for i := 0; i < n; i++ {
			at, _, ok := q.Pop()
			if !ok {
				t.Fatalf("trial %d: expected %d events, queue emptied early at i=%d", trial, n, i)
			}
			if at != times[i] {
				t.Fatalf("trial %d: position %d: expected timestamp %d, got %d", trial, i, times[i], at)
			}
		}
		if _, _, ok := q.Pop(); ok {
			t.Fatalf("trial %d: expected the queue to be empty after popping all %d events", trial, n)
		}
	}
}
