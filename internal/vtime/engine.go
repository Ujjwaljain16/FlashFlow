package vtime

import (
	"fmt"

	"flashflow/internal/clock"
)

// DefaultMaxEvents caps how many events a single Run call will process
// before giving up and returning an error. This is the engine's defense
// against an infinite zero-time event loop (a callback that keeps
// rescheduling more work at a virtual timestamp already reached,
// forever) — the engine cannot distinguish that from "a very large but
// finite experiment" except by a bound. Override with SetMaxEvents if a
// specific experiment genuinely needs more.
const DefaultMaxEvents = 1_000_000

// Engine drives a virtual clock by processing scheduled events instead
// of sleeping: pop the earliest event, advance the clock to exactly that
// event's timestamp, execute its callback (which may itself schedule
// more events, never earlier than the time just reached), and repeat.
//
// The clock and event queue are fully owned by the Engine — nothing
// outside it can advance the clock or reach into the queue directly.
// That is what makes the determinism guarantee possible: if only the
// engine's own loop ever moves time forward or decides execution order,
// there is no second path through which nondeterminism (a stray
// goroutine, a direct clock mutation) could enter. Domain objects that
// need to read the current time (cache.Cache, health.Registry, ...) are
// handed the read-only view from Clock(), never the Engine itself.
type Engine struct {
	clock *clock.MockClock
	queue *EventQueue
	trace Trace

	processed uint64
	maxEvents uint64
}

// NewEngine creates an Engine whose virtual clock starts at start.
func NewEngine(start clock.VirtualTime) *Engine {
	return &Engine{
		clock:     clock.NewMockClock(start),
		queue:     NewEventQueue(),
		maxEvents: DefaultMaxEvents,
	}
}

// Record appends one entry to the engine's trace, stamped with the
// engine's current virtual time. Call this from event callbacks to build
// up the experiment's execution history — see TraceEvent's doc comment
// for what belongs in typ/entity/fields.
func (e *Engine) Record(typ, entity string, fields map[string]any) {
	e.trace.record(e.clock.Now(), typ, entity, fields)
}

// Trace returns the engine's recorded trace so far.
func (e *Engine) Trace() *Trace {
	return &e.trace
}

// SetMaxEvents overrides DefaultMaxEvents.
func (e *Engine) SetMaxEvents(n uint64) {
	e.maxEvents = n
}

// Clock returns the engine's virtual clock as a read-only clock.Clock,
// for constructing domain objects that need to observe the same
// timeline the engine advances. Only the engine itself ever calls
// Advance/AdvanceTo on the underlying MockClock.
func (e *Engine) Clock() clock.Clock {
	return e.clock
}

// Now returns the engine's current virtual time.
func (e *Engine) Now() clock.VirtualTime {
	return e.clock.Now()
}

// ProcessedCount returns how many events this engine has executed over
// its lifetime, across every Run call — useful for experiment metadata
// (an event trace's summary) and for debugging a run that hit
// DefaultMaxEvents.
func (e *Engine) ProcessedCount() uint64 {
	return e.processed
}

// Schedule schedules callback to run at virtual time at. Returns an
// error if at is before the engine's current time — see
// MockClock.AdvanceTo's doc comment for why that is rejected outright
// rather than silently clamped or reordered.
func (e *Engine) Schedule(at clock.VirtualTime, callback Callback) (EventID, error) {
	now := e.clock.Now()
	if at < now {
		return 0, fmt.Errorf("vtime: cannot schedule an event %v before the current time (at=%d, now=%d)", now.Sub(at), at, now)
	}
	return e.queue.Schedule(at, callback), nil
}

// Cancel cancels a previously scheduled event. See EventQueue.Cancel.
func (e *Engine) Cancel(id EventID) bool {
	return e.queue.Cancel(id)
}

// RunUntilEmpty processes events until none remain, including any
// scheduled by callbacks along the way. Returns an error if the engine's
// max-event bound is exceeded first — most likely a callback
// rescheduling work without ever making the queue shrink, an infinite
// event loop rather than a large but finite experiment.
func (e *Engine) RunUntilEmpty() error {
	for {
		at, cb, ok := e.queue.Pop()
		if !ok {
			return nil
		}
		if err := e.step(at, cb); err != nil {
			return err
		}
	}
}

// RunUntil processes every pending event scheduled at or before t, then
// advances the clock to exactly t — even if the last event executed was
// earlier than t, or no events existed to process at all. Events
// scheduled beyond t are left in the queue for a later Run call, not
// discarded. Returns an error if t is before the engine's current time.
func (e *Engine) RunUntil(t clock.VirtualTime) error {
	now := e.clock.Now()
	if t < now {
		return fmt.Errorf("vtime: RunUntil(%d) is %v before the current time %d", t, now.Sub(t), now)
	}
	for {
		at, ok := e.queue.Peek()
		if !ok || at > t {
			break
		}
		_, cb, _ := e.queue.Pop()
		if err := e.step(at, cb); err != nil {
			return err
		}
	}
	return e.clock.AdvanceTo(t)
}

// step advances the clock to at and executes cb, enforcing maxEvents.
func (e *Engine) step(at clock.VirtualTime, cb Callback) error {
	if e.processed >= e.maxEvents {
		return fmt.Errorf("vtime: exceeded max event count (%d) — likely an infinite same-time event loop", e.maxEvents)
	}
	if err := e.clock.AdvanceTo(at); err != nil {
		// Should be unreachable: the queue only ever hands back events at
		// or after the time we last advanced to. Surfaced explicitly
		// rather than panicking, in case a future change breaks that.
		return fmt.Errorf("vtime: internal invariant violated advancing to a popped event's time: %w", err)
	}
	e.processed++
	cb()
	return nil
}
