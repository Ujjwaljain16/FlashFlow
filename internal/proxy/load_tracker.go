package proxy

import "sync"

// LoadTracker measures application-level in-flight request count per
// target: requests the proxy has dispatched but not yet gotten an outcome
// for. Deliberately NOT derived from transport.TrackedTransport.ActiveConns
// — that counts physical TCP sockets, which under HTTP/1.1 keep-alive
// systematically undercounts true concurrent load (one pooled socket
// serially carries many requests). See docs/learning/002-http-reverse-proxy.md,
// "Critical Discovery for Stage 3".
//
// Proxy-owned ambient instrumentation: ReverseProxy increments/decrements
// this around every request regardless of which TargetSelector is active,
// so any selector can read current load without reimplementing lifecycle
// bookkeeping.
type LoadTracker struct {
	mu       sync.RWMutex
	inFlight map[string]int64
}

func NewLoadTracker() *LoadTracker {
	return &LoadTracker{inFlight: make(map[string]int64)}
}

func (t *LoadTracker) Increment(target string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inFlight[target]++
}

// Decrement is floored at zero: under correct proxy usage it's never
// called more than Increment for the same target (ServeHTTP pairs every
// Increment with one deferred Decrement covering all exit paths), but the
// floor guards against ever exposing a nonsensical negative count if that
// invariant is violated.
func (t *LoadTracker) Decrement(target string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inFlight[target] > 0 {
		t.inFlight[target]--
	}
}

// Get is a pure read with a known, bounded race: a caller that reads Get
// for several candidates and only afterwards calls Increment (exactly what
// LeastConnectionsSelector + ServeHTTP do, as two separate steps) can have
// several concurrent callers make the same "least loaded" decision from
// stale counts before any of their increments are visible to each other.
// Self-corrects on the next selection; counts themselves never corrupt.
// Deliberately not engineered around here — Experiment 003-C measures
// whether it's observable in practice rather than assuming it away.
func (t *LoadTracker) Get(target string) int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.inFlight[target]
}

// Snapshot is an independent copy, safe to mutate.
func (t *LoadTracker) Snapshot() map[string]int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]int64, len(t.inFlight))
	for k, v := range t.inFlight {
		out[k] = v
	}
	return out
}
