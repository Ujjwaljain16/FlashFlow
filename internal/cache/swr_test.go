package cache

import (
	"sync"
	"testing"
	"time"

	"flashflow/internal/clock"
)

func TestGetSWR_MissWhenAbsent(t *testing.T) {
	c := NewWithConfig(clock.NewMockClock(0), Config{TTL: time.Second, StaleWindow: time.Second}, NewCoalescer())
	_, result := c.GetSWR("k", func() (Entry, error) { return Entry{}, nil })
	if result != Miss {
		t.Fatalf("got %v, want Miss", result)
	}
}

func TestGetSWR_FreshWithinTTL(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := NewWithConfig(mc, Config{TTL: time.Second, StaleWindow: time.Second}, NewCoalescer())
	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	mc.Advance(500 * time.Millisecond) // still within TTL
	entry, result := c.GetSWR("k", func() (Entry, error) { t.Fatal("revalidate should not be called on a Fresh hit"); return Entry{}, nil })
	if result != Fresh {
		t.Fatalf("got %v, want Fresh", result)
	}
	if entry.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", entry.StatusCode)
	}
}

func TestGetSWR_StaleWithinWindow_ServesImmediatelyAndRevalidates(t *testing.T) {
	mc := clock.NewMockClock(0)
	co := NewCoalescer()
	c := NewWithConfig(mc, Config{TTL: 100 * time.Millisecond, StaleWindow: 200 * time.Millisecond}, co)
	c.Set("k", &Entry{StatusCode: 200, Body: []byte("old"), StoredAt: mc.Now()})

	mc.Advance(150 * time.Millisecond) // past TTL (100ms), within TTL+StaleWindow (300ms)

	var revalidateCalled sync.WaitGroup
	revalidateCalled.Add(1)
	entry, result := c.GetSWR("k", func() (Entry, error) {
		defer revalidateCalled.Done()
		return Entry{StatusCode: 200, Body: []byte("new")}, nil
	})
	if result != Stale {
		t.Fatalf("got %v, want Stale", result)
	}
	if string(entry.Body) != "old" {
		t.Errorf("expected the STALE (old) entry to be served immediately, got %q", entry.Body)
	}

	revalidateCalled.Wait() // background revalidation must actually fire

	// Poll briefly for the background goroutine's Set to land -- it runs
	// concurrently with this test after revalidate() itself returns.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.RLock()
		body := string(c.entries["k"].Body)
		c.mu.RUnlock()
		if body == "new" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("expected the background revalidation to eventually replace the cached entry with the fresh value")
}

func TestGetSWR_PastStaleWindowIsMiss(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := NewWithConfig(mc, Config{TTL: 100 * time.Millisecond, StaleWindow: 200 * time.Millisecond}, NewCoalescer())
	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	mc.Advance(400 * time.Millisecond) // past TTL+StaleWindow (300ms)
	_, result := c.GetSWR("k", func() (Entry, error) { return Entry{}, nil })
	if result != Miss {
		t.Fatalf("got %v, want Miss", result)
	}
}

func TestGetSWR_NoCoalescerDisablesStaleServing(t *testing.T) {
	mc := clock.NewMockClock(0)
	// StaleWindow > 0 but coalescer is nil -- GetSWR must fall back to
	// treating a past-TTL entry as a plain Miss, per its own documented
	// contract, rather than firing an uncoalesced background goroutine.
	c := NewWithConfig(mc, Config{TTL: 100 * time.Millisecond, StaleWindow: 200 * time.Millisecond}, nil)
	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	mc.Advance(150 * time.Millisecond)
	_, result := c.GetSWR("k", func() (Entry, error) {
		t.Fatal("revalidate should never be called without a coalescer")
		return Entry{}, nil
	})
	if result != Miss {
		t.Fatalf("got %v, want Miss (no coalescer configured)", result)
	}
}

func TestGetSWR_ZeroStaleWindowMatchesPlainGet(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, 100*time.Millisecond) // StaleWindow defaults to 0 via New
	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	mc.Advance(150 * time.Millisecond) // past TTL
	_, result := c.GetSWR("k", func() (Entry, error) {
		t.Fatal("revalidate should never be called with StaleWindow=0")
		return Entry{}, nil
	})
	if result != Miss {
		t.Fatalf("got %v, want Miss (StaleWindow=0 means no stale serving, matching Get's own past-TTL behavior)", result)
	}
}

func TestGetSWR_ConcurrentStaleHitsCoalesceIntoOneRevalidation(t *testing.T) {
	mc := clock.NewMockClock(0)
	co := NewCoalescer()
	c := NewWithConfig(mc, Config{TTL: 50 * time.Millisecond, StaleWindow: 500 * time.Millisecond}, co)
	c.Set("k", &Entry{StatusCode: 200, Body: []byte("old"), StoredAt: mc.Now()})
	mc.Advance(100 * time.Millisecond)

	var calls int
	var mu sync.Mutex
	slowRevalidate := func() (Entry, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(150 * time.Millisecond) // hold the in-flight window open so concurrent stale hits (each firing its own background goroutine) actually overlap it
		return Entry{StatusCode: 200, Body: []byte("new")}, nil
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // maximize how simultaneously all 20 calls (and the background goroutines each spawns) actually launch
			_, result := c.GetSWR("k", slowRevalidate)
			if result != Stale {
				t.Errorf("got %v, want Stale", result)
			}
		}()
	}
	close(start)
	wg.Wait()
	time.Sleep(300 * time.Millisecond) // let any in-flight background goroutines finish

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("expected exactly 1 real revalidate() call across 20 concurrent stale hits, got %d", calls)
	}
}
