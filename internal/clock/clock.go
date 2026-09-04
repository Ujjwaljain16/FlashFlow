package clock

import "time"

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
