package clock

import (
	"fmt"
	"sync"
	"time"
)

// VirtualTime represents time as integer nanoseconds from an epoch.
// All domain state machines, timestamps, and intervals in FlashFlow
// are expressed in VirtualTime.
type VirtualTime int64

// Nanoseconds returns the integer nanosecond representation of the VirtualTime.
func (v VirtualTime) Nanoseconds() int64 {
	return int64(v)
}

// Sub returns the duration difference v - u.
func (v VirtualTime) Sub(u VirtualTime) time.Duration {
	return time.Duration(v - u)
}

// Add returns v advanced by d. d may be negative — computing a past
// timestamp is fine; what an event queue must reject is the *clock*
// itself moving backward (see MockClock.AdvanceTo), not this arithmetic.
func (v VirtualTime) Add(d time.Duration) VirtualTime {
	return v + VirtualTime(d.Nanoseconds())
}

// Clock provides time abstraction for domain logic without coupling to system time.
type Clock interface {
	Now() VirtualTime
}

// WallClock implements Clock using the system wall clock.
type WallClock struct{}

// NewWallClock creates a WallClock.
func NewWallClock() WallClock {
	return WallClock{}
}

// Now returns the current system wall-clock time as VirtualTime.
func (WallClock) Now() VirtualTime {
	return VirtualTime(time.Now().UnixNano())
}

// MockClock is a manually-advanced Clock for deterministic tests and for
// the virtual-time engine — anything whose correctness depends on elapsed
// time (e.g. cache TTL expiration) needs to control time directly rather
// than sleeping and hoping. Zero value starts at VirtualTime 0; use
// NewMockClock to start at a specific time.
//
// Mutex-guarded because existing real-engine callers already read a
// shared MockClock from concurrent request-handling goroutines (see
// experiments/004-caching-failures) while advancing it from the test's
// own goroutine — a real cross-goroutine access pattern, not a
// virtual-engine concern. The virtual engine itself is single-threaded by
// design (see Engine's doc comment) and never contends on this lock.
type MockClock struct {
	mu      sync.Mutex
	current VirtualTime
}

// NewMockClock creates a MockClock starting at start.
func NewMockClock(start VirtualTime) *MockClock {
	return &MockClock{current: start}
}

// Now returns the clock's current (manually set) time.
func (m *MockClock) Now() VirtualTime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Advance moves the clock forward by d. d must not be negative — use
// AdvanceTo if you need to reason in terms of an absolute target time
// instead of a relative step.
func (m *MockClock) Advance(d time.Duration) {
	if d < 0 {
		panic(fmt.Sprintf("clock: Advance called with negative duration %v", d))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = m.current.Add(d)
}

// AdvanceTo moves the clock forward to exactly t — the operation a
// discrete-event engine actually needs ("jump to the next scheduled
// event's timestamp"), as opposed to Advance's relative-step form.
//
// t must not be before the clock's current time. Silently clamping or
// ignoring a backward jump would let an event execute as if time had
// gone in reverse relative to events already processed, corrupting the
// experiment's causal history without any visible symptom — exactly the
// "time-travel bug" this engine is designed to make impossible rather
// than merely unlikely. Returns an error instead.
func (m *MockClock) AdvanceTo(t VirtualTime) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t < m.current {
		return fmt.Errorf("clock: cannot advance backward from %d to %d", m.current, t)
	}
	m.current = t
	return nil
}
