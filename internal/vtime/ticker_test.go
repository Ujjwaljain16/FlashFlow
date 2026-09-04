package vtime

import (
	"testing"
	"time"

	"flashflow/internal/clock"
)

func TestTicker_FiresAtStartThenEveryInterval(t *testing.T) {
	e := NewEngine(0)
	var fires []clock.VirtualTime

	start := clock.VirtualTime(100 * time.Millisecond)
	ticker, err := e.NewTicker(start, 50*time.Millisecond, func() {
		fires = append(fires, e.Now())
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ticker.Stop()

	// Run until just past the 4th expected firing (100, 150, 200, 250ms) --
	// the ticker keeps rescheduling itself beyond that, RunUntil's horizon
	// is what bounds how many of those get processed here.
	if err := e.RunUntil(clock.VirtualTime(260 * time.Millisecond)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []clock.VirtualTime{
		clock.VirtualTime(100 * time.Millisecond),
		clock.VirtualTime(150 * time.Millisecond),
		clock.VirtualTime(200 * time.Millisecond),
		clock.VirtualTime(250 * time.Millisecond),
	}
	if len(fires) != len(want) {
		t.Fatalf("expected %d fires, got %d: %v", len(want), len(fires), fires)
	}
	for i := range want {
		if fires[i] != want[i] {
			t.Fatalf("fire %d: expected %d, got %d", i, want[i], fires[i])
		}
	}
}

func TestTicker_StopPreventsFurtherFiring(t *testing.T) {
	e := NewEngine(0)
	count := 0

	ticker, err := e.NewTicker(0, 10*time.Millisecond, func() {
		count++
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Let it fire a few times, then stop it mid-run via a one-shot event.
	e.Schedule(clock.VirtualTime(35*time.Millisecond), func() {
		ticker.Stop()
	})

	if err := e.RunUntil(clock.VirtualTime(200 * time.Millisecond)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fires expected at 0, 10, 20, 30 before the stop event at 35 runs
	// (same-timestamp tie-break: the stop event and any tick at exactly
	// 35 would order by schedule sequence, but 35 isn't a tick time here).
	if count != 4 {
		t.Fatalf("expected exactly 4 fires before Stop, got %d", count)
	}
}

func TestTicker_StopFromWithinItsOwnCallback(t *testing.T) {
	e := NewEngine(0)
	count := 0
	var ticker *Ticker
	var err error
	ticker, err = e.NewTicker(0, 10*time.Millisecond, func() {
		count++
		if count == 3 {
			ticker.Stop()
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if count != 3 {
		t.Fatalf("expected exactly 3 fires (stopped from within the 3rd), got %d", count)
	}
}

func TestTicker_DoubleStopIsSafe(t *testing.T) {
	e := NewEngine(0)
	ticker, err := e.NewTicker(0, 10*time.Millisecond, func() {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ticker.Stop()
	ticker.Stop() // must not panic or double-cancel an already-cancelled event
}
