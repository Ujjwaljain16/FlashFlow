package vtime

import (
	"time"

	"flashflow/internal/clock"
)

// Ticker is the event-driven equivalent of time.Ticker: it fires
// callback repeatedly, interval virtual time apart, starting at start.
// Each firing schedules the next one itself — there is no real
// background goroutine, just ordinary recursive scheduling on the same
// engine loop that runs everything else, which is what keeps the whole
// scenario deterministic and single-threaded.
type Ticker struct {
	engine   *Engine
	interval time.Duration
	callback Callback

	stopped bool
	current EventID
}

// NewTicker creates and starts a Ticker on e: callback first fires at
// start, then every interval afterward, until Stop is called. Returns an
// error if start is before e's current time (see Engine.Schedule).
func (e *Engine) NewTicker(start clock.VirtualTime, interval time.Duration, callback Callback) (*Ticker, error) {
	t := &Ticker{engine: e, interval: interval, callback: callback}
	id, err := e.Schedule(start, t.fire)
	if err != nil {
		return nil, err
	}
	t.current = id
	return t, nil
}

func (t *Ticker) fire() {
	if t.stopped {
		return
	}
	t.callback()
	if t.stopped {
		// callback itself may have called Stop -- don't reschedule.
		return
	}
	next := t.engine.Now().Add(t.interval)
	// Scheduling from the engine's own current time always succeeds
	// (Engine.Schedule only rejects times before now), so the error is
	// unreachable here — not ignored, just structurally impossible.
	id, _ := t.engine.Schedule(next, t.fire)
	t.current = id
}

// Stop halts the ticker. If called from inside the ticker's own
// callback, the ticker fires one last time (the call already in
// progress) and then stops rescheduling itself — it does not retract a
// firing already underway.
func (t *Ticker) Stop() {
	if t.stopped {
		return
	}
	t.stopped = true
	t.engine.Cancel(t.current)
}
