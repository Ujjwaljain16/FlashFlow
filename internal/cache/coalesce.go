package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// call is one in-flight, shared upstream fetch for a single key.
type call struct {
	wg  sync.WaitGroup
	val Entry
	err error
}

// CoalesceStats is a point-in-time snapshot of coalescing activity. Leads
// and Shared both count individual Do calls (not distinct fetches) — a
// stampede of 100 callers for one key produces 1 Lead and 99 Shared, all
// against the single fetch the Lead actually performed.
type CoalesceStats struct {
	Leads    uint64 `json:"leads"`
	Shared   uint64 `json:"shared"`
	Failures uint64 `json:"failures"`
}

// Coalescer deduplicates concurrent fetches for the same key: the first
// caller for a key becomes the leader and actually runs fn; any other
// caller for the same key that arrives before the leader's fn returns
// becomes a waiter and receives the leader's exact result — value or
// error — without running fn itself.
//
// The most important invariant: a completed or failed fn call must never
// leave an abandoned in-flight entry that blocks future requests forever.
// The in-flight entry is removed the moment fn returns — success, error,
// or panic. A panic in fn is recovered and turned into a shared error
// rather than left to hang every waiter and crash the leader's goroutine.
//
// Coalescer has no opinion about what context fn uses internally — that
// is the caller's responsibility, and it matters: if fn is built from one
// specific caller's own request context, cancelling that one caller (its
// client disconnects, say) would cancel the fetch for every waiter behind
// it too. See EdgeServer's use of this type for why the shared fetch
// deliberately runs with an independent context instead.
type Coalescer struct {
	mu    sync.Mutex
	calls map[string]*call

	leads    atomic.Uint64
	shared   atomic.Uint64
	failures atomic.Uint64
}

// NewCoalescer creates an empty Coalescer.
func NewCoalescer() *Coalescer {
	return &Coalescer{calls: make(map[string]*call)}
}

// Do executes fn for key, or waits for and returns the result of an
// already-in-flight fn call for the same key. shared reports whether this
// call waited for someone else's fetch (true) or was the leader that
// actually performed it (false).
func (c *Coalescer) Do(key string, fn func() (Entry, error)) (entry Entry, err error, shared bool) {
	c.mu.Lock()
	if inFlight, ok := c.calls[key]; ok {
		c.mu.Unlock()
		inFlight.wg.Wait()
		c.shared.Add(1)
		if inFlight.err != nil {
			c.failures.Add(1)
		}
		return inFlight.val, inFlight.err, true
	}

	cl := &call{}
	cl.wg.Add(1)
	c.calls[key] = cl
	c.mu.Unlock()

	func() {
		defer func() {
			if r := recover(); r != nil {
				cl.err = fmt.Errorf("coalesced fetch panicked: %v", r)
			}
		}()
		cl.val, cl.err = fn()
	}()

	// Signal waiters before removing the in-flight entry (canonical
	// singleflight ordering) -- the reverse order left a narrow window
	// where a new caller arriving between the delete and the Done() would
	// see no in-flight entry and start a redundant fetch instead of
	// piggy-backing on the result that was about to become available.
	cl.wg.Done()
	c.mu.Lock()
	delete(c.calls, key)
	c.mu.Unlock()

	c.leads.Add(1)
	if cl.err != nil {
		c.failures.Add(1)
	}

	return cl.val, cl.err, false
}

// Snapshot returns a point-in-time copy of the activity counters.
func (c *Coalescer) Snapshot() CoalesceStats {
	return CoalesceStats{
		Leads:    c.leads.Load(),
		Shared:   c.shared.Load(),
		Failures: c.failures.Load(),
	}
}
