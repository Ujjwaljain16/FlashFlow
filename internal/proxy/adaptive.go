package proxy

import (
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"flashflow/internal/clock"
)

// AdaptiveWeights are the explicit, visible weights combining each
// signal into one score. All weights are expected to be non-negative;
// their exact magnitudes are a policy choice, not a claim of
// optimality — see docs/learning/007-adaptive-routing-replay.md for how
// the defaults were selected and which scenarios they were checked
// against. Automated tuning of these values is Stage 8's job, not this
// one's.
type AdaptiveWeights struct {
	Load    float64 // weight for (1 - utilization), utilization = load/capacity
	Latency float64 // weight for (1 - normalized latency); neutral when unobserved or stale
	Cache   float64 // weight for cache affinity: 1 if this target last served the request's key
	Cost    float64 // weight for (1 - normalized configured cost)
}

// DefaultAdaptiveWeights is a starting point, not a claim of optimality.
func DefaultAdaptiveWeights() AdaptiveWeights {
	return AdaptiveWeights{Load: 0.4, Latency: 0.4, Cache: 0.1, Cost: 0.1}
}

// AdaptiveConfig bundles the weights with the two parameters the score
// needs beyond raw signal values.
type AdaptiveConfig struct {
	Weights AdaptiveWeights
	// ReferenceLatency is the latency at which the latency signal is
	// "50% penalized" (see scoreLatency) — a bounded ratio transform,
	// not an arbitrary hard maximum.
	ReferenceLatency time.Duration
	// StaleAfter: a latency observation older than this (time since the
	// target was last selected, used as a proxy for "time since we could
	// have observed it") is treated as cold-start neutral rather than
	// trusted — the direct fix for the staleness blind spot Stage 3/6
	// proved EWMA has.
	StaleAfter time.Duration
}

// DefaultAdaptiveConfig is a starting point, not a claim of optimality.
func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		Weights:          DefaultAdaptiveWeights(),
		ReferenceLatency: 100 * time.Millisecond,
		StaleAfter:       1 * time.Second,
	}
}

// TargetScore is one target's full signal breakdown for one decision —
// the explainability record item 22/53 in the Stage 7 design ask for.
// Every field is in [0,1] except CombinedScore, which is the weighted
// sum of the other four (so it can exceed 1 if weights sum above 1;
// weights aren't required to sum to 1, only to be non-negative and
// consistent in direction).
type TargetScore struct {
	Target        string
	Utilization   float64 // higher = less loaded relative to capacity
	LatencyScore  float64 // higher = lower/better latency, or neutral (0.5) if unobserved/stale
	CacheScore    float64 // 1 if this target last served the request's key, else 0
	CostScore     float64 // higher = cheaper relative to configured cost
	CombinedScore float64
}

// AdaptiveSelector implements TargetSelector using a weighted
// combination of four signals. Health is deliberately NOT one of them:
// eligibility filtering (excluding unhealthy targets from `available`)
// already happens upstream — in both the real proxy (ServeHTTP filters
// by registry.IsAvailable before calling any selector) and the virtual
// routing harnesses (005-G's identical pattern) — before SelectTarget is
// ever invoked. Re-checking health here would be exactly the
// duplicated-detection mistake the Stage 7 design explicitly warns
// against; this selector trusts `available` completely.
//
// Capacity is likewise not a separate additive term: it normalizes Load
// into a utilization ratio (load/capacity) rather than standing alone.
// That is a direct synthesis of Stage 3's own conclusion (see
// docs/learning/003-routing-policies.md §7): a configured weight (WRR)
// encodes capacity but goes stale the moment reality changes, while a
// live signal (load) is self-correcting but blind to how much capacity
// a target actually has. Utilization combines both properties into one
// signal instead of two.
type AdaptiveSelector struct {
	mu             sync.RWMutex
	loadTracker    *LoadTracker
	latencyTracker *LatencyTracker
	capacity       TargetWeights
	cost           TargetWeights
	clock          clock.Clock
	cfg            AdaptiveConfig

	lastSelected map[string]clock.VirtualTime // staleness: when this target was last chosen
	keyAffinity  map[string]string            // cache signal: request key -> target that last served it
}

// NewAdaptiveSelector's loadTracker/latencyTracker should be the same
// proxy-owned instances every other Stage 3 selector reads, so switching
// to Adaptive doesn't lose observation history. capacity/cost default to
// weight 1 for any target not present in the map — an unconfigured
// target is treated as average, not penalized for missing configuration.
func NewAdaptiveSelector(loadTracker *LoadTracker, latencyTracker *LatencyTracker, capacity, cost TargetWeights, clk clock.Clock, cfg AdaptiveConfig) *AdaptiveSelector {
	if clk == nil {
		clk = clock.NewWallClock()
	}
	if capacity == nil {
		capacity = TargetWeights{}
	}
	if cost == nil {
		cost = TargetWeights{}
	}
	return &AdaptiveSelector{
		loadTracker: loadTracker, latencyTracker: latencyTracker,
		capacity: capacity, cost: cost, clock: clk, cfg: cfg,
		lastSelected: make(map[string]clock.VirtualTime),
		keyAffinity:  make(map[string]string),
	}
}

// requestKey mirrors cache.Key's method+path(+query) convention, without
// importing internal/cache — the adaptive router's affinity concept
// doesn't need to know how the actual cache stores entries, only that
// two requests share a key.
func requestKey(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if r.URL.RawQuery == "" {
		return r.Method + " " + r.URL.Path
	}
	return r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
}

// scoreUtilization returns 1 - min(1, load/capacity): an idle target
// scores close to 1, a target at or beyond its configured capacity
// scores 0.
func (s *AdaptiveSelector) scoreUtilization(target string) float64 {
	load := s.loadTracker.Get(target)
	cap, configured := s.capacity[target]
	switch {
	case !configured:
		// No entry at all: unconfigured, treated as average (weight 1) —
		// see NewAdaptiveSelector's doc comment.
		cap = 1
	case cap <= 0:
		// A target explicitly configured with capacity<=0 means "always
		// treat as fully utilized" (score 0 for this signal), not
		// "unconfigured" — collapsing these two cases previously let an
		// operator's explicit "pull this target from rotation" capacity
		// setting be silently treated as merely average.
		return 0
	}
	utilization := float64(load) / float64(cap)
	if utilization > 1 {
		utilization = 1
	}
	return 1 - utilization
}

// scoreLatency returns a value in [0,1], 1 meaning no latency penalty at
// all, via the ratio transform latency/(latency+ReferenceLatency) —
// bounded and monotonic without needing an arbitrary hard maximum the
// way min-max normalization would.
//
// An unobserved or stale estimate returns exactly 0.5: deliberately
// neutral, not optimistic. Contrast EWMASelector's "unobserved beats
// observed" cold-start rule, which Experiment 006-B proved causes
// deterministic winner-take-all lock-in among literally equal targets —
// a brand-new or long-unselected target should compete on its other
// signals here, not automatically win or automatically lose on this one.
func (s *AdaptiveSelector) scoreLatency(target string, now clock.VirtualTime) float64 {
	estimate, ok := s.latencyTracker.Estimate(target)
	if !ok {
		return 0.5 // cold start: never observed at all
	}
	// Only discount data we have positive evidence is stale (this
	// selector previously selected it and enough time has passed since).
	// If we have no staleness record at all -- e.g. right after swapping
	// from another selector onto an already-warm shared LatencyTracker --
	// trust the tracker's existing estimate rather than assuming
	// staleness we can't actually verify.
	if last, seen := s.lastSelected[target]; seen && now.Sub(last) > s.cfg.StaleAfter {
		return 0.5
	}
	ref := float64(s.cfg.ReferenceLatency)
	if ref <= 0 {
		// A zero/negative ReferenceLatency (reachable if a caller builds
		// AdaptiveConfig{} directly instead of via DefaultAdaptiveConfig()
		// or the tuner's bounded ConfigSpace) would otherwise make
		// est/(est+ref) evaluate to 0/0 = NaN whenever the target's own
		// estimate is also exactly zero. Treat it the same as "no signal"
		// rather than let a malformed config poison the score.
		return 0.5
	}
	est := float64(estimate)
	return 1 - est/(est+ref)
}

// scoreCache returns 1 if target most recently served this exact
// request key through this selector, else 0 — an affinity hint the
// router maintains from its own routing history, not an introspection
// of what the target's real cache actually still holds (which may have
// since expired or been evicted independently).
func (s *AdaptiveSelector) scoreCache(target, key string) float64 {
	if key == "" {
		return 0
	}
	if last, ok := s.keyAffinity[key]; ok && last == target {
		return 1
	}
	return 0
}

// scoreCost returns 1 - normalized configured cost, min-max across the
// configured cost map. Min-max is safe here specifically because cost is
// a design-time constant, not noisy live data — unlike latency,
// exaggerating small differences between fixed configuration values
// isn't a real risk.
func (s *AdaptiveSelector) scoreCost(target string) float64 {
	if len(s.cost) == 0 {
		return 1 // nobody has a configured cost -- no penalty for anyone
	}
	cost, ok := s.cost[target]
	if !ok {
		cost = 1
	}
	min, max := s.costRange()
	if max == min {
		return 1
	}
	return 1 - float64(cost-min)/float64(max-min)
}

func (s *AdaptiveSelector) costRange() (min, max int) {
	first := true
	for _, c := range s.cost {
		if first {
			min, max = c, c
			first = false
			continue
		}
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	return min, max
}

// score computes the full weighted combination for one target.
func (s *AdaptiveSelector) score(target, key string, now clock.VirtualTime) TargetScore {
	u := s.scoreUtilization(target)
	l := s.scoreLatency(target, now)
	c := s.scoreCache(target, key)
	co := s.scoreCost(target)
	w := s.cfg.Weights
	return TargetScore{
		Target: target, Utilization: u, LatencyScore: l, CacheScore: c, CostScore: co,
		CombinedScore: w.Load*u + w.Latency*l + w.Cache*c + w.Cost*co,
	}
}

// SelectTarget implements TargetSelector.
func (s *AdaptiveSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}

	// Scoring only reads state (lastSelected/keyAffinity lookups,
	// capacity/cost/tracker reads) -- a read lock lets concurrent
	// SelectTarget calls score candidates in parallel; only the two map
	// writes at the end need exclusive access.
	s.mu.RLock()
	now := s.clock.Now()
	key := requestKey(r)

	// Sorted so the tie-break below is deterministic (alphabetically
	// first among equal top scores) rather than dependent on the order
	// `available` happened to arrive in — critical for counterfactual
	// replay, where map/slice ordering must never be the source of a
	// difference between runs.
	candidates := append([]string(nil), available...)
	sort.Strings(candidates)

	best := candidates[0]
	bestScore := s.score(best, key, now).CombinedScore
	for _, t := range candidates[1:] {
		sc := s.score(t, key, now).CombinedScore
		// A non-finite score (should be unreachable given scoreLatency's
		// own NaN guard, but defense-in-depth: Go's NaN comparisons are
		// always false, so a NaN bestScore would otherwise permanently
		// freeze selection on whichever candidate happened to be first —
		// never disqualify a later, real-valued candidate on that basis).
		if math.IsNaN(bestScore) || (!math.IsNaN(sc) && sc > bestScore) {
			bestScore = sc
			best = t
		}
	}
	s.mu.RUnlock()

	s.mu.Lock()
	s.lastSelected[best] = now
	s.keyAffinity[key] = best
	s.mu.Unlock()
	return best, nil
}

// Explain returns the full per-target signal breakdown SelectTarget
// would use for this request, without selecting anything or mutating
// any state — for tests, tracing, and experiment analysis that need to
// answer "why did target B win" without re-deciding.
func (s *AdaptiveSelector) Explain(r *http.Request, available []string) []TargetScore {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock.Now()
	key := requestKey(r)

	out := make([]TargetScore, 0, len(available))
	for _, t := range available {
		out = append(out, s.score(t, key, now))
	}
	return out
}
