package vtime

import (
	"testing"
	"time"

	"flashflow/internal/clock"
)

func TestEngine_RunUntilEmpty_ProcessesEventsInOrder(t *testing.T) {
	e := NewEngine(0)
	var order []clock.VirtualTime
	e.Schedule(30, func() { order = append(order, e.Now()) })
	e.Schedule(10, func() { order = append(order, e.Now()) })
	e.Schedule(20, func() { order = append(order, e.Now()) })

	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []clock.VirtualTime{10, 20, 30}
	if len(order) != len(want) {
		t.Fatalf("expected %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, order)
		}
	}
	if e.Now() != 30 {
		t.Fatalf("expected the engine's clock to end at the last event's time (30), got %d", e.Now())
	}
	if e.ProcessedCount() != 3 {
		t.Fatalf("expected ProcessedCount 3, got %d", e.ProcessedCount())
	}
}

func TestEngine_ScheduleRejectsThePast(t *testing.T) {
	e := NewEngine(1000)
	if _, err := e.Schedule(500, func() {}); err == nil {
		t.Fatal("expected Schedule to reject a time before the current clock")
	}
}

func TestEngine_ScheduleAllowsExactlyNow(t *testing.T) {
	e := NewEngine(1000)
	if _, err := e.Schedule(1000, func() {}); err != nil {
		t.Fatalf("expected scheduling exactly at the current time to succeed, got %v", err)
	}
}

func TestEngine_CancelPreventsExecution(t *testing.T) {
	e := NewEngine(0)
	ran := false
	id, err := e.Schedule(10, func() { ran = true })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !e.Cancel(id) {
		t.Fatal("expected Cancel to succeed")
	}
	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran {
		t.Fatal("expected the cancelled event to never run")
	}
}

func TestEngine_RunUntil_AdvancesExactlyToTWithNoEvents(t *testing.T) {
	e := NewEngine(0)
	if err := e.RunUntil(500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Now() != 500 {
		t.Fatalf("expected clock at 500 with no events at all, got %d", e.Now())
	}
}

func TestEngine_RunUntil_LeavesLaterEventsPending(t *testing.T) {
	e := NewEngine(0)
	var ranEarly, ranLate bool
	e.Schedule(100, func() { ranEarly = true })
	e.Schedule(2000, func() { ranLate = true })

	if err := e.RunUntil(500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ranEarly {
		t.Fatal("expected the t=100 event to have run")
	}
	if ranLate {
		t.Fatal("expected the t=2000 event to not have run yet")
	}
	if e.Now() != 500 {
		t.Fatalf("expected clock at 500, got %d", e.Now())
	}

	if err := e.RunUntil(2000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ranLate {
		t.Fatal("expected the t=2000 event to have run after RunUntil(2000)")
	}
}

func TestEngine_RunUntil_RejectsTimeBeforeCurrent(t *testing.T) {
	e := NewEngine(1000)
	if err := e.RunUntil(500); err == nil {
		t.Fatal("expected an error running until a time before the current clock")
	}
}

func TestEngine_ClockReflectsEngineAdvancement(t *testing.T) {
	e := NewEngine(0)
	c := e.Clock()
	if c.Now() != 0 {
		t.Fatalf("expected 0, got %d", c.Now())
	}
	e.Schedule(500, func() {})
	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Now() != 500 {
		t.Fatalf("expected the Clock() view to reflect engine advancement, got %d", c.Now())
	}
}

// TestEngine_MaxEventsGuardsInfiniteZeroTimeLoop is the "infinite
// zero-time loop" failure mode item 69 in the Stage 5 spec calls out
// explicitly: a callback that keeps rescheduling more work at a
// timestamp already reached must not hang the engine forever.
func TestEngine_MaxEventsGuardsInfiniteZeroTimeLoop(t *testing.T) {
	e := NewEngine(0)
	e.SetMaxEvents(100)

	count := 0
	var reschedule Callback
	reschedule = func() {
		count++
		e.Schedule(e.Now(), reschedule) // always at the current time -- never makes progress
	}
	e.Schedule(0, reschedule)

	err := e.RunUntilEmpty()
	if err == nil {
		t.Fatal("expected RunUntilEmpty to return an error for an infinite zero-time loop")
	}
	if count != 100 {
		t.Fatalf("expected exactly maxEvents (100) executions before stopping, got %d", count)
	}
}

// TestEngine_VirtualTimeAdvancesWithoutRealSleeping is Phase E's proof
// requirement: a scenario spanning a large virtual duration must not
// cost anywhere near that much real wall-clock time. time.Now/Since here
// are test-verification code measuring the test's own real performance,
// not experiment-domain logic -- the legitimate exception item 58 carves
// out.
func TestEngine_VirtualTimeAdvancesWithoutRealSleeping(t *testing.T) {
	e := NewEngine(0)
	tenMinutes := clock.VirtualTime((10 * time.Minute).Nanoseconds())
	e.Schedule(tenMinutes, func() {})

	realStart := time.Now()
	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	realElapsed := time.Since(realStart)

	if e.Now() != tenMinutes {
		t.Fatalf("expected the virtual clock to read %d (10 minutes), got %d", tenMinutes, e.Now())
	}
	if realElapsed > 100*time.Millisecond {
		t.Fatalf("expected negligible real time to process one event 10 virtual minutes out, took %v", realElapsed)
	}
}

// TestEngine_CallbackCanScheduleMoreEvents proves the core recursive
// property the whole engine depends on: a callback scheduling further
// work gets picked up and ordered correctly by the same running loop,
// not lost or requiring a second Run call.
func TestEngine_CallbackCanScheduleMoreEvents(t *testing.T) {
	e := NewEngine(0)
	var order []clock.VirtualTime

	e.Schedule(10, func() {
		order = append(order, e.Now())
		e.Schedule(e.Now()+20, func() {
			order = append(order, e.Now())
		})
	})

	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []clock.VirtualTime{10, 30}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("expected %v, got %v", want, order)
	}
}
