// Package vtime is FlashFlow's deterministic virtual-time execution
// machinery: an ordered event queue and (once integrated, see the
// engine's own doc comment) an engine that drives a clock.MockClock by
// processing events instead of sleeping.
//
// This package deliberately has almost no concurrency of its own. A
// discrete-event simulation is fundamentally a single ordered history —
// one event executes, possibly scheduling more, then the next earliest
// event executes. Modeling that with real goroutines and channels would
// reintroduce exactly the nondeterminism (Go runtime scheduling order)
// this package exists to eliminate. See EventQueue and the engine type
// for where "simulated concurrency" (overlapping virtual-time intervals)
// is represented instead as ordinary sequential state, not real parallel
// execution.
package vtime

import (
	"container/heap"

	"flashflow/internal/clock"
)

// EventID identifies one scheduled event, returned by Schedule and
// consumed by Cancel.
type EventID uint64

// Callback is the work to run when a scheduled event's time arrives.
type Callback func()

// entry is one scheduled item. seq is the deterministic tie-breaker for
// events scheduled at the identical virtual time — see EventQueue's doc
// comment on why this matters scientifically, not just as an
// implementation detail.
type entry struct {
	at        clock.VirtualTime
	seq       uint64
	id        EventID
	callback  Callback
	cancelled bool
	index     int // heap.Interface bookkeeping; unused once popped
}

// eventHeap implements container/heap.Interface, ordered by (at, seq).
type eventHeap []*entry

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *eventHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}
func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// EventQueue is a deterministic, ordered queue of scheduled callbacks.
//
// Ordering is (timestamp, insertion sequence): two events scheduled for
// the identical virtual time always execute in the order Schedule was
// called, never in whatever order Go's map iteration or goroutine
// scheduling happens to produce on a given run. This is not an
// implementation nicety — Stage 5's whole premise is that same-timestamp
// event order is part of the experiment's definition (a cache expiring
// and a request arriving at the same virtual millisecond can produce a
// hit or a miss depending on which happens "first"), so it must be a
// documented, tested rule, not an accident of runtime luck.
//
// EventQueue has no notion of "now": it never reads a clock, never
// validates that a scheduled time is in the future, and never executes
// anything itself. That is deliberate — it keeps this type a pure,
// independently testable data structure. The engine that owns a
// clock.MockClock (see the engine type built on top of this package) is
// what decides what "scheduled in the past" means and actually invokes
// each callback once popped.
type EventQueue struct {
	h       eventHeap
	byID    map[EventID]*entry
	nextSeq uint64
	nextID  EventID
}

// NewEventQueue creates an empty EventQueue.
func NewEventQueue() *EventQueue {
	return &EventQueue{byID: make(map[EventID]*entry)}
}

// Schedule adds callback to run at virtual time at, returning an EventID
// Cancel can later use. Two events scheduled for the same at run in the
// exact order Schedule was called.
func (q *EventQueue) Schedule(at clock.VirtualTime, callback Callback) EventID {
	q.nextID++
	id := q.nextID
	e := &entry{at: at, seq: q.nextSeq, id: id, callback: callback}
	q.nextSeq++
	heap.Push(&q.h, e)
	q.byID[id] = e
	return id
}

// Cancel marks a previously scheduled event so it will never be
// returned by Pop. Returns false if id is unknown, already cancelled, or
// already popped — cancelling something that can no longer run is not an
// error, just a no-op the caller can observe if it cares.
func (q *EventQueue) Cancel(id EventID) bool {
	e, ok := q.byID[id]
	if !ok || e.cancelled {
		return false
	}
	e.cancelled = true
	delete(q.byID, id)
	return true
}

// Len returns the number of pending, not-cancelled, not-yet-popped
// events. A cancelled entry is removed from this count immediately by
// Cancel even though it may still sit in the underlying heap until it
// would otherwise have been popped (lazy removal) — Len reflects logical
// pending work, not raw heap size.
func (q *EventQueue) Len() int {
	return len(q.byID)
}

// Peek returns the next not-cancelled event's scheduled time without
// removing it, and false if no such event remains.
func (q *EventQueue) Peek() (clock.VirtualTime, bool) {
	q.dropCancelledFront()
	if len(q.h) == 0 {
		return 0, false
	}
	return q.h[0].at, true
}

// Pop removes and returns the earliest not-cancelled event's scheduled
// time and callback, and false if no such event remains.
func (q *EventQueue) Pop() (clock.VirtualTime, Callback, bool) {
	q.dropCancelledFront()
	if len(q.h) == 0 {
		return 0, nil, false
	}
	e := heap.Pop(&q.h).(*entry)
	delete(q.byID, e.id)
	return e.at, e.callback, true
}

// dropCancelledFront discards cancelled entries sitting at the heap's
// root. Only the front needs checking on each call: a cancelled entry
// buried deeper stays put harmlessly until it bubbles to the front on
// some later Pop/Peek, where this same cleanup catches it then.
func (q *EventQueue) dropCancelledFront() {
	for len(q.h) > 0 && q.h[0].cancelled {
		heap.Pop(&q.h)
	}
}
