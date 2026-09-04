package cache

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"flashflow/internal/clock"
)

func TestCache_MissOnEmpty(t *testing.T) {
	c := New(clock.NewMockClock(0), time.Second)
	_, ok := c.Get("missing")
	if ok {
		t.Fatalf("expected miss on empty cache")
	}
	stats := c.Snapshot()
	if stats.Lookups != 1 || stats.Misses != 1 || stats.Hits != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestCache_SetThenHit(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, time.Second)

	entry := &Entry{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"ok":true}`), StoredAt: mc.Now()}
	c.Set("GET /data", entry)

	got, ok := c.Get("GET /data")
	if !ok {
		t.Fatalf("expected hit after Set")
	}
	if got.StatusCode != 200 || string(got.Body) != `{"ok":true}` || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected entry contents: %+v", got)
	}

	stats := c.Snapshot()
	if stats.Fills != 1 || stats.Hits != 1 || stats.Misses != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestCache_ExpiredEntryIsMiss(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, 10*time.Millisecond)

	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	// Still fresh just before TTL.
	mc.Advance(9 * time.Millisecond)
	if _, ok := c.Get("k"); !ok {
		t.Fatalf("expected hit before TTL elapses")
	}

	// Now past TTL.
	mc.Advance(2 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatalf("expected miss once TTL has elapsed")
	}

	stats := c.Snapshot()
	if stats.Expired != 1 {
		t.Fatalf("expected exactly 1 expired entry recorded, got stats=%+v", stats)
	}
}

// TestCache_ExpiredEntryIsLazilyEvicted verifies the map itself shrinks —
// not just that Get reports a miss — proving expired entries don't sit
// around forever consuming memory.
func TestCache_ExpiredEntryIsLazilyEvicted(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, 10*time.Millisecond)
	c.Set("k", &Entry{StatusCode: 200, StoredAt: mc.Now()})

	c.mu.RLock()
	_, presentBefore := c.entries["k"]
	c.mu.RUnlock()
	if !presentBefore {
		t.Fatalf("expected entry present immediately after Set")
	}

	mc.Advance(20 * time.Millisecond)
	c.Get("k") // triggers lazy eviction

	c.mu.RLock()
	_, presentAfter := c.entries["k"]
	c.mu.RUnlock()
	if presentAfter {
		t.Fatalf("expected expired entry to be evicted from the map after Get, but it is still present")
	}
}

func TestCache_SetOverwritesExistingEntry(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, time.Second)
	c.Set("k", &Entry{StatusCode: 200, Body: []byte("old"), StoredAt: mc.Now()})
	c.Set("k", &Entry{StatusCode: 200, Body: []byte("new"), StoredAt: mc.Now()})

	got, ok := c.Get("k")
	if !ok {
		t.Fatalf("expected hit")
	}
	if string(got.Body) != "new" {
		t.Fatalf("expected the second Set to win, got body %q", got.Body)
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	mc := clock.NewMockClock(0)
	c := New(mc, time.Second)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(n int) {
			defer wg.Done()
			key := Key("GET", "/data", "")
			c.Set(key, &Entry{StatusCode: 200, Body: []byte("x"), StoredAt: mc.Now()})
			c.Get(key)
			c.Get("never-set-key")
		}(g)
	}
	wg.Wait()

	stats := c.Snapshot()
	if stats.Fills != goroutines {
		t.Fatalf("expected %d fills, got %d", goroutines, stats.Fills)
	}
	if stats.Lookups != goroutines*2 {
		t.Fatalf("expected %d lookups, got %d", goroutines*2, stats.Lookups)
	}
}

func TestKey_ExcludesHeadersIncludesQuery(t *testing.T) {
	if Key("GET", "/data", "") != "GET /data" {
		t.Fatalf("unexpected key without query: %q", Key("GET", "/data", ""))
	}
	if Key("GET", "/data", "id=5") != "GET /data?id=5" {
		t.Fatalf("unexpected key with query: %q", Key("GET", "/data", "id=5"))
	}
	if Key("GET", "/data", "") == Key("POST", "/data", "") {
		t.Fatalf("expected different methods to produce different keys")
	}
}
