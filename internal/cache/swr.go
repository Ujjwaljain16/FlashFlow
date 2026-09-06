package cache

// GetResult reports which of three states GetSWR found key in.
type GetResult int

const (
	Miss  GetResult = iota // not present, or past TTL+StaleWindow -- caller must fetch synchronously
	Fresh                  // present and within TTL -- identical to a plain Get hit
	Stale                  // present, past TTL but within TTL+StaleWindow -- served immediately, one background revalidation already fired
)

// GetSWR is Get's Stale-While-Revalidate-aware sibling (Stage 10,
// §10.5 -- the PRD's fourth named cache policy, "SWR," missing per the
// Stage 8 audit's F-09). An entry within TTL is Fresh, exactly like a
// plain Get hit. An entry past TTL but within TTL+StaleWindow is
// served immediately as Stale -- the caller gets a usable response
// without waiting on a synchronous origin round trip -- while exactly
// one background revalidation is fired via the Cache's own Coalescer,
// so a burst of concurrent stale hits for the same key triggers one
// real revalidate() call, not one per caller (the same deduplication
// Coalescer already gives the synchronous miss path). An entry past
// TTL+StaleWindow (or the Cache has no StaleWindow/Coalescer
// configured) is a plain Miss, evicted exactly like Get's own lazy
// expiry.
//
// revalidate is called at most once per background revalidation (never
// on the Fresh or Miss paths, and never synchronously from this call --
// GetSWR itself never blocks on it). It should return the freshly
// fetched Entry; GetSWR stamps StoredAt itself before storing, so
// revalidate does not need to set it.
func (c *Cache) GetSWR(key string, revalidate func() (Entry, error)) (*Entry, GetResult) {
	c.lookups.Add(1)

	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.misses.Add(1)
		return nil, Miss
	}

	age := c.clock.Now().Sub(e.StoredAt)
	if age < c.ttl {
		c.hits.Add(1)
		return e, Fresh
	}

	if c.staleWindow > 0 && c.coalescer != nil && age < c.ttl+c.staleWindow {
		c.hits.Add(1)
		c.staleHits.Add(1)
		go c.revalidateInBackground(key, revalidate)
		return e, Stale
	}

	// Fully expired (or SWR not configured for this Cache): evict and
	// miss, exactly matching Get's own lazy-eviction behavior.
	c.expired.Add(1)
	c.misses.Add(1)
	c.mu.Lock()
	if cur, stillPresent := c.entries[key]; stillPresent && cur == e {
		delete(c.entries, key)
	}
	c.mu.Unlock()
	return nil, Miss
}

// revalidateInBackground runs revalidate through the Cache's Coalescer
// (deduplicating concurrent stale-triggered revalidations for the same
// key into one real call) and stores its result. A failed revalidation
// leaves the stale entry in place untouched -- it will be retried on
// the next stale hit, or age past TTL+StaleWindow into a real Miss if
// nothing ever succeeds.
func (c *Cache) revalidateInBackground(key string, revalidate func() (Entry, error)) {
	entry, err, _ := c.coalescer.Do(key, revalidate)
	if err != nil {
		return
	}
	entry.StoredAt = c.clock.Now()
	c.Set(key, &entry)
}
