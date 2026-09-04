package proxy

import (
	"math/rand"
	"net/http"
	"sync"
)

// P2CScorer returns a comparable score for target (lower is better), with
// ok=false meaning "no data yet" — treated as strictly better than any
// real score (see preferScore), same cold-start convention as EWMASelector.
type P2CScorer func(target string) (score float64, ok bool)

// ScorerFromLatency uses LatencyTracker's EWMA estimate (ok=false until
// first observed).
func ScorerFromLatency(tracker *LatencyTracker) P2CScorer {
	return func(target string) (float64, bool) {
		latency, ok := tracker.Estimate(target)
		return float64(latency), ok
	}
}

// ScorerFromLoad uses LoadTracker's in-flight count. Always ok=true — 0 is
// a real, valid count, not "no data"; there's no cold-start concept here.
func ScorerFromLoad(tracker *LoadTracker) P2CScorer {
	return func(target string) (float64, bool) {
		return float64(tracker.Get(target)), true
	}
}

// P2CSelector implements Power of Two Choices: sample two distinct
// candidates uniformly at random and pick the better by scorer.
//
// Why: Experiment 003-D found LeastConnectionsSelector (under sustained
// low-concurrency ties) and EWMASelector (past cold-start, any
// concurrency) can permanently lock onto one of several genuinely equal
// targets, because full-scan argmin compares every contender against a
// single current best — the instant one loses, it's never selected (and
// so never re-observed) again. Comparing a fresh random pair each time
// fixes that: an equally-good target keeps winning roughly half the
// comparisons it's part of, so it stays fresh instead of getting frozen
// out by one noisy early sample. See Experiment 003-E for the before/after.
//
// Boundary of that fix: Observe/Increment only ever fire for the winner
// of a comparison, never the loser. A target that's genuinely worse than
// its rivals still never wins, so it's never re-observed — same staleness
// as full-scan argmin. This lands differently by scorer: ScorerFromLoad
// is never actually stale, since an idle target's true in-flight count is
// always 0 (live state, not a memory) — a long-idle target is correctly
// eligible again immediately. ScorerFromLatency has no such ground truth:
// an unselected target's estimate is a memory with no way to check if
// it's still accurate, so it can never discover a target it stopped
// choosing has recovered, same blind spot as EWMA. Fixing that needs an
// explicit re-probing budget — out of scope for Stage 3 (Stage 3 README).
//
// Randomness is explicit and injected (see NewP2CSelector), not
// math/rand's globals, so behavior is reproducible under a recorded seed.
type P2CSelector struct {
	mu     sync.Mutex
	rng    *rand.Rand
	scorer P2CScorer
}

// NewP2CSelector's rng must not be nil; pass rand.New(rand.NewSource(seed))
// with a recorded seed for reproducible behavior.
func NewP2CSelector(scorer P2CScorer, rng *rand.Rand) *P2CSelector {
	return &P2CSelector{scorer: scorer, rng: rng}
}

// SelectTarget implements TargetSelector.
func (s *P2CSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	switch len(available) {
	case 0:
		return "", ErrNoHealthyTargets
	case 1:
		return available[0], nil
	}

	i, j := s.samplePair(len(available))
	a, b := available[i], available[j]

	aScore, aOK := s.scorer(a)
	bScore, bOK := s.scorer(b)

	if preferScore(bScore, bOK, aScore, aOK) {
		return b, nil
	}
	return a, nil
}

// samplePair picks 2 distinct indices in [0,n) uniformly from all C(n,2)
// pairs; n>=2 (SelectTarget's caller guarantees this). Mutex-guarded
// because *rand.Rand isn't safe for concurrent use.
func (s *P2CSelector) samplePair(n int) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := s.rng.Intn(n)
	j := s.rng.Intn(n - 1)
	if j >= i {
		j++
	}
	return i, j
}

// preferScore: no-data beats data; otherwise lower score wins and ties
// keep the current best. A tie is unbiased here because the caller always
// passes a freshly randomized pair — unlike a fixed `available`-order
// tie-break, no target is systematically favored.
func preferScore(score float64, ok bool, bestScore float64, bestOK bool) bool {
	if ok != bestOK {
		return !ok
	}
	if !ok {
		return false
	}
	return score < bestScore
}
