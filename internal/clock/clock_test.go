package clock

import (
	"testing"
	"time"
)

func TestWallClock_Now(t *testing.T) {
	wc := NewWallClock()
	t1 := wc.Now()
	time.Sleep(2 * time.Millisecond)
	t2 := wc.Now()

	if t2 <= t1 {
		t.Fatalf("expected t2 > t1, got t1=%d, t2=%d", t1, t2)
	}

	diff := t2.Sub(t1)
	if diff < 1*time.Millisecond {
		t.Fatalf("expected diff >= 1ms, got %v", diff)
	}
}

func TestMockClock_DeterministicAdvance(t *testing.T) {
	mc := NewMockClock(1000)
	if mc.Now() != 1000 {
		t.Fatalf("expected 1000, got %d", mc.Now())
	}

	mc.Advance(500 * time.Millisecond)
	expected := VirtualTime(1000 + (500 * time.Millisecond).Nanoseconds())
	if mc.Now() != expected {
		t.Fatalf("expected %d, got %d", expected, mc.Now())
	}
}

func TestMockClock_AdvanceNegativePanics(t *testing.T) {
	mc := NewMockClock(1000)
	defer func() {
		if recover() == nil {
			t.Fatal("expected Advance with a negative duration to panic")
		}
	}()
	mc.Advance(-1 * time.Millisecond)
}

func TestMockClock_AdvanceToJumpsForward(t *testing.T) {
	mc := NewMockClock(1000)
	if err := mc.AdvanceTo(5000); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mc.Now() != 5000 {
		t.Fatalf("expected 5000, got %d", mc.Now())
	}
}

func TestMockClock_AdvanceToSameTimeIsANoOp(t *testing.T) {
	mc := NewMockClock(1000)
	if err := mc.AdvanceTo(1000); err != nil {
		t.Fatalf("expected advancing to the current time to succeed as a no-op, got %v", err)
	}
	if mc.Now() != 1000 {
		t.Fatalf("expected 1000, got %d", mc.Now())
	}
}

func TestMockClock_AdvanceToBackwardIsRejected(t *testing.T) {
	mc := NewMockClock(5000)
	err := mc.AdvanceTo(1000)
	if err == nil {
		t.Fatal("expected an error moving the clock backward")
	}
	if mc.Now() != 5000 {
		t.Fatalf("expected a rejected AdvanceTo to leave the clock unchanged at 5000, got %d", mc.Now())
	}
}

func TestVirtualTime_Add(t *testing.T) {
	v := VirtualTime(1000)
	if got := v.Add(500 * time.Millisecond); got != VirtualTime(1000+(500*time.Millisecond).Nanoseconds()) {
		t.Fatalf("unexpected result: %d", got)
	}
	// A negative duration computes a past timestamp -- this is plain
	// arithmetic, distinct from AdvanceTo rejecting the *clock* itself
	// moving backward.
	if got := v.Add(-500 * time.Millisecond); got != VirtualTime(1000-(500*time.Millisecond).Nanoseconds()) {
		t.Fatalf("unexpected result for negative delta: %d", got)
	}
}
