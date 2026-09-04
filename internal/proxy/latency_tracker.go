package proxy

import (
	"sync"
	"time"
)

// defaultEWMAAlpha (0.2) is used when NewLatencyTracker gets an invalid
// alpha — each observation then shifts the estimate 20% of the way toward
// it, roughly a 5-request averaging window.
const defaultEWMAAlpha = 0.2

// LatencyTracker maintains an EWMA of upstream response latency per
// target: the duration from just before dispatch (T2) to upstream response
// received (T3) — see ReverseProxy.ServeHTTP. Excludes proxy-side work and
// response-body streaming; may include cold-connection dial time folded
// into a sample with no way to distinguish it from a warm one. Accepted
// imprecision for Stage 3 — see the Stage 3 README.
//
// Only successful round trips produce an observation. A failure never
// updates the estimate: a target that's outright failing is health's
// concern (it gets marked UNHEALTHY and excluded before any selector sees
// it), not a latency-preference one — conflating the two would duplicate
// responsibility that already lives elsewhere.
//
// Proxy-owned ambient instrumentation, same pattern as LoadTracker: an
// observation is recorded after every successful round trip regardless of
// which selector is active, so history survives a selector swap.
type LatencyTracker struct {
	mu       sync.RWMutex
	alpha    float64
	estimate map[string]time.Duration
	seen     map[string]bool
}

// NewLatencyTracker's alpha (weight given to each new observation) must be
// in (0, 1]; outside that range it's coerced to defaultEWMAAlpha rather
// than producing nonsensical estimates (alpha<=0 never updates past the
// first sample; alpha>1 overshoots and can flip the estimate's sign).
func NewLatencyTracker(alpha float64) *LatencyTracker {
	if alpha <= 0 || alpha > 1 {
		alpha = defaultEWMAAlpha
	}
	return &LatencyTracker{
		alpha:    alpha,
		estimate: make(map[string]time.Duration),
		seen:     make(map[string]bool),
	}
}

// Observe applies estimate = alpha*sample + (1-alpha)*estimate; the first
// observation for a target seeds the estimate directly (no prior to blend).
func (t *LatencyTracker) Observe(target string, latency time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.seen[target] {
		t.estimate[target] = latency
		t.seen[target] = true
		return
	}
	prev := t.estimate[target]
	t.estimate[target] = time.Duration(t.alpha*float64(latency) + (1-t.alpha)*float64(prev))
}

// Estimate's ok is false for a never-observed target — callers (see
// EWMASelector) must treat that as distinct from "observed as instant".
func (t *LatencyTracker) Estimate(target string) (latency time.Duration, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.estimate[target], t.seen[target]
}

// Snapshot is an independent copy, safe to mutate; only includes targets
// with at least one observation.
func (t *LatencyTracker) Snapshot() map[string]time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]time.Duration, len(t.estimate))
	for k, v := range t.estimate {
		out[k] = v
	}
	return out
}
