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
