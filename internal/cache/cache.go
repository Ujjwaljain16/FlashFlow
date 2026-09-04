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

// Key returns the cache key for a method+path+query. Deliberately
// excludes headers — including X-Request-ID, for example, would make
// every request its own unique key and defeat caching entirely. Callers
// decide which methods are worth looking up at all; Key itself doesn't
// enforce a scope.
func Key(method, path, rawQuery string) string {
	if rawQuery == "" {
		return method + " " + path
	}
	return method + " " + path + "?" + rawQuery
}

// Stats is a point-in-time snapshot of cache activity counters.
type Stats struct {
	Lookups uint64 `json:"lookups"`
	Hits    uint64 `json:"hits"`
	Misses  uint64 `json:"misses"`
	Expired uint64 `json:"expired"`
	Fills   uint64 `json:"fills"`
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
	mu    sync.RWMutex
	clock clock.Clock
	ttl   time.Duration

	entries map[string]*Entry

	lookups atomic.Uint64
	hits    atomic.Uint64
	misses  atomic.Uint64
	expired atomic.Uint64
	fills   atomic.Uint64
}

// New creates a Cache with the given fixed TTL. clk must not be nil.
func New(clk clock.Clock, ttl time.Duration) *Cache {
	return &Cache{
		clock:   clk,
		ttl:     ttl,
		entries: make(map[string]*Entry),
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
		Lookups: c.lookups.Load(),
		Hits:    c.hits.Load(),
		Misses:  c.misses.Load(),
		Expired: c.expired.Load(),
		Fills:   c.fills.Load(),
	}
}
