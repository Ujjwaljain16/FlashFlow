package cache

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"flashflow/internal/clock"
)

// Entry is one cached HTTP response: exactly the fields needed to
// reconstruct a valid response (status, end-to-end headers, body), plus
// when it was stored. TTL lives on the Cache, not the Entry — see Cache's
// doc comment for why.
type Entry struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	StoredAt   clock.VirtualTime
}

// Key returns the cache key for a method+path+query, optionally folding in
// extra caller-supplied dimensions. Headers are deliberately excluded by
// default — including X-Request-ID, for example, would make every request
// its own unique key and defeat caching entirely — but a caller whose
// origin varies its *response* based on a specific request header (e.g. a
// debug/override header) must pass that header's value via extra, or two
// requests differing only in that header will collide on one cache entry
// and serve each other's response. Callers decide which methods are worth
// looking up at all, and which headers (if any) are response-determining;
// Key itself doesn't enforce either.
func Key(method, path, rawQuery string, extra ...string) string {
	key := method + " " + path
	if rawQuery != "" {
		key += "?" + rawQuery
	}
	for _, e := range extra {
		if e != "" {
			key += "|" + e
		}
	}
	return key
}

// Stats is a point-in-time snapshot of cache activity counters.
type Stats struct {
	Lookups   uint64 `json:"lookups"`
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Expired   uint64 `json:"expired"`
	Fills     uint64 `json:"fills"`
	StaleHits uint64 `json:"stale_hits"` // subset of Hits served from GetSWR's stale window -- see swr.go
}

// Cache is a fixed-TTL, unbounded response cache — no eviction policy
// yet, since nothing planned for this stage's experiments needs one (see
// the Stage 4 README for when LRU would actually get justified). TTL is
// one fixed duration per Cache rather than parsed from Cache-Control,
// because Origin doesn't send cache headers and full RFC cache semantics
// (ETag, Vary, max-age) aren't needed by any experiment planned here.
//
// An expired entry is detected and evicted lazily, on the next Get for
// that key — not by a background sweeper. That sidesteps giving this
// type its own timer/goroutine lifecycle to manage for no real benefit
// at this scale.
//
// Time comes from an injected clock.Clock, not time.Now(), so TTL logic
// isn't hard-wired to wall-clock time before Stage 5's virtual-time
// engine exists to make that actually matter.
type Cache struct {
	mu          sync.RWMutex
	clock       clock.Clock
	ttl         time.Duration
	staleWindow time.Duration // 0 disables SWR entirely -- see swr.go
	coalescer   *Coalescer    // required for SWR's background revalidation to be deduplicated; nil disables SWR even if staleWindow > 0

	entries map[string]*Entry

	lookups   atomic.Uint64
	hits      atomic.Uint64
	misses    atomic.Uint64
	expired   atomic.Uint64
	fills     atomic.Uint64
	staleHits atomic.Uint64
}

// New creates a Cache with the given fixed TTL and no SWR support --
// equivalent to NewWithConfig(clk, Config{TTL: ttl}, nil). Kept as its
// own function since it's what every existing caller before Stage 10
// already uses by name.
func New(clk clock.Clock, ttl time.Duration) *Cache {
	return NewWithConfig(clk, Config{TTL: ttl}, nil)
}

// Config configures a Cache, including the optional Stale-While-
// Revalidate window Stage 10 (§10.5) adds.
type Config struct {
	TTL time.Duration
	// StaleWindow, when > 0, lets GetSWR serve an entry for up to this
	// long past TTL expiry while firing one background revalidation --
	// 0 (the default) means "today's behavior": an entry past TTL is a
	// plain miss.
	StaleWindow time.Duration
}

// NewWithConfig creates a Cache from cfg. coalescer must be non-nil for
// GetSWR's stale-serving behavior to actually activate (see GetSWR's
// doc comment) -- passing nil with a non-zero StaleWindow does not
// error, it just means GetSWR never serves stale (falls back to
// today's fresh-or-miss behavior), since firing uncoalesced background
// revalidations for every stale hit under concurrent load would be
// its own, worse problem (a thundering herd of redundant origin
// fetches, exactly what Coalescer exists to prevent on the synchronous
// miss path already).
func NewWithConfig(clk clock.Clock, cfg Config, coalescer *Coalescer) *Cache {
	return &Cache{
		clock:       clk,
		ttl:         cfg.TTL,
		staleWindow: cfg.StaleWindow,
		coalescer:   coalescer,
		entries:     make(map[string]*Entry),
	}
}

// Get looks up key. A present-but-expired entry counts as a miss and is
// evicted as part of this call, rather than left in the map for a later
// Snapshot to see as if it were still live.
func (c *Cache) Get(key string) (*Entry, bool) {
	c.lookups.Add(1)

	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	if c.clock.Now().Sub(e.StoredAt) >= c.ttl {
		c.expired.Add(1)
		c.misses.Add(1)
		c.mu.Lock()
		// Re-check under the write lock: a concurrent Set for the same
		// key may have already replaced this exact entry since the read
		// above. Only delete if it's still the one we saw expire, so we
		// can't clobber a fresher concurrent write.
		if cur, stillPresent := c.entries[key]; stillPresent && cur == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}

	c.hits.Add(1)
	return e, true
}

// Set stores entry under key, replacing any existing entry.
func (c *Cache) Set(key string, entry *Entry) {
	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
	c.fills.Add(1)
}

// Snapshot returns a point-in-time copy of the activity counters.
func (c *Cache) Snapshot() Stats {
	return Stats{
		Lookups:   c.lookups.Load(),
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Expired:   c.expired.Load(),
		Fills:     c.fills.Load(),
		StaleHits: c.staleHits.Load(),
	}
}
