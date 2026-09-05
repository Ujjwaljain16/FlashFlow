package challenge

import (
	"fmt"
	"testing"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/clock"
)

// TestCacheChallenge_HotKey confirms a single frequently-requested key
// stays correctly served from cache across many repeated lookups, and
// that its popularity doesn't disturb unrelated cold keys' own
// correctness -- the "one hot key among many cold ones" shape real
// traffic actually has, as opposed to every prior cache test's uniform
// access pattern.
func TestCacheChallenge_HotKey(t *testing.T) {
	clk := clock.NewMockClock(0)
	c := cache.New(clk, 10*time.Second)

	c.Set("/hot", &cache.Entry{StatusCode: 200, Body: []byte("hot"), StoredAt: clk.Now()})
	for i := 0; i < 5; i++ {
		c.Set(fmt.Sprintf("/cold-%d", i), &cache.Entry{StatusCode: 200, Body: []byte("cold"), StoredAt: clk.Now()})
	}

	for i := 0; i < 1000; i++ {
		e, ok := c.Get("/hot")
		if !ok || string(e.Body) != "hot" {
			t.Fatalf("lookup %d: expected a stable hit on the hot key, got ok=%v body=%q", i, ok, e)
		}
	}
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("/cold-%d", i)
		e, ok := c.Get(key)
		if !ok || string(e.Body) != "cold" {
			t.Fatalf("cold key %s: expected an unaffected hit, got ok=%v", key, ok)
		}
	}

	stats := c.Snapshot()
	if stats.Hits != 1005 { // 1000 hot + 5 cold
		t.Fatalf("expected 1005 hits, got %d", stats.Hits)
	}
	if stats.Misses != 0 {
		t.Fatalf("expected 0 misses, got %d", stats.Misses)
	}
}

// TestCacheChallenge_ColdCacheBurst confirms a burst of many distinct,
// never-before-seen keys arriving at once all correctly miss, and all
// correctly hit once filled -- the empty-cache startup case, as opposed
// to every prior test's small, pre-populated key set.
func TestCacheChallenge_ColdCacheBurst(t *testing.T) {
	clk := clock.NewMockClock(0)
	c := cache.New(clk, 10*time.Second)

	const n = 2000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/burst-%d", i)
		if _, ok := c.Get(key); ok {
			t.Fatalf("key %s: expected a miss on a never-before-seen key in a cold cache", key)
		}
		c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v"), StoredAt: clk.Now()})
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/burst-%d", i)
		if _, ok := c.Get(key); !ok {
			t.Fatalf("key %s: expected a hit immediately after being filled", key)
		}
	}

	stats := c.Snapshot()
	if stats.Misses != n {
		t.Fatalf("expected %d misses (one per key, first access), got %d", n, stats.Misses)
	}
	if stats.Hits != n {
		t.Fatalf("expected %d hits (one per key, second access), got %d", n, stats.Hits)
	}
	if stats.Fills != n {
		t.Fatalf("expected %d fills, got %d", n, stats.Fills)
	}
}

// TestCacheChallenge_SynchronizedExpiry confirms a "thundering herd" of
// keys all stored at the identical instant with the identical TTL all
// expire together, precisely at the TTL boundary -- not staggered, not
// early, not late. This is the scenario a cache warmed by one bulk
// operation (a deploy, a cold-start prefetch) actually produces.
func TestCacheChallenge_SynchronizedExpiry(t *testing.T) {
	clk := clock.NewMockClock(0)
	const ttl = 5 * time.Second
	c := cache.New(clk, ttl)

	const n = 500
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/sync-%d", i)
		c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v"), StoredAt: clk.Now()})
	}

	// Just before the boundary: every key must still hit.
	clk.Advance(ttl - time.Millisecond)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/sync-%d", i)
		if _, ok := c.Get(key); !ok {
			t.Fatalf("key %s: expected a hit 1ms before TTL expiry", key)
		}
	}

	// At/past the boundary: every key must now miss, together.
	clk.Advance(2 * time.Millisecond)
	expiredCount := 0
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/sync-%d", i)
		if _, ok := c.Get(key); !ok {
			expiredCount++
		}
	}
	if expiredCount != n {
		t.Fatalf("expected all %d synchronized entries to expire together at the TTL boundary, got %d", n, expiredCount)
	}
}

// TestCacheChallenge_HugeWorkingSet confirms basic correctness --not
// performance-- holds at a working-set size two orders of magnitude
// larger than any other cache test in this project: every key set is
// independently retrievable, and Snapshot's counters remain exact.
// internal/cache.Cache is documented as deliberately unbounded (no
// eviction policy); this test doesn't challenge that design choice, it
// confirms the design choice doesn't silently break correctness at
// scale.
func TestCacheChallenge_HugeWorkingSet(t *testing.T) {
	clk := clock.NewMockClock(0)
	c := cache.New(clk, time.Hour)

	const n = 10000
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("/huge-%d", i)
		c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte(fmt.Sprintf("body-%d", i)), StoredAt: clk.Now()})
	}

	// Spot-check across the range (first, middle, last, and a scattered
	// sample) rather than exhaustively re-reading all 10,000 -- enough
	// to catch a hashing, sizing, or off-by-one correctness bug without
	// making this test itself slow.
	sample := []int{0, 1, n / 4, n / 2, 3 * n / 4, n - 2, n - 1}
	for _, i := range sample {
		key := fmt.Sprintf("/huge-%d", i)
		e, ok := c.Get(key)
		if !ok {
			t.Fatalf("key %s: expected a hit at working-set size %d", key, n)
		}
		want := fmt.Sprintf("body-%d", i)
		if string(e.Body) != want {
			t.Fatalf("key %s: expected body %q, got %q -- possible key collision at scale", key, want, e.Body)
		}
	}

	stats := c.Snapshot()
	if stats.Fills != n {
		t.Fatalf("expected %d fills, got %d", n, stats.Fills)
	}
}
