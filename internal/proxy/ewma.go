package proxy

import (
	"net/http"
)

// EWMASelector prefers the available target with the lowest EWMA latency
// estimate (shared LatencyTracker, updated by ReverseProxy — see its doc
// comment for exactly what's measured).
//
// Cold-start rule: an unobserved target beats any observed one, forcing
// every target to be tried at least once before real numbers are compared
// — a direct response to Experiment 003-C, where LeastConnectionsSelector
// gets stuck forever on whichever target is first in `available` under
// sustained ties (low concurrency). Ties among unobserved or numerically
// equal targets fall back to `available` order, but only after every
// target has already had its one guaranteed try.
//
// KNOWN LIMITATION (Experiment 003-D, left unfixed deliberately — see the
// Stage 3 README §H4): past cold-start this is pure-greedy with no ongoing
// exploration. Among genuinely equal targets, whichever wins the first few
// noisy samples wins every selection after — the losers stop being
// selected, so they stop being observed, so their estimate freezes
// forever. Consequence: it can lock onto a wildly uneven split (e.g.
// 100/0/0) among interchangeable targets, and it can never detect that an
// unselected target's true performance has since changed. This is the
// evidence-based motivation for P2C's random sampling (Experiment 003-E).
type EWMASelector struct {
	tracker *LatencyTracker
}

// NewEWMASelector's tracker must be the same *LatencyTracker instance the
// proxy updates (ReverseProxy.LatencyTracker(), attached via SetSelector).
func NewEWMASelector(tracker *LatencyTracker) *EWMASelector {
	return &EWMASelector{tracker: tracker}
}

// SelectTarget implements TargetSelector.
func (s *EWMASelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}

	best := available[0]
	bestLatency, bestOK := s.tracker.Estimate(best)
	bestScore := float64(bestLatency)
	for _, t := range available[1:] {
		latency, ok := s.tracker.Estimate(t)
		score := float64(latency)
		// preferScore (p2c.go) is shared with P2CSelector rather than
		// duplicated — same unobserved-beats-observed / lower-wins rule.
		if preferScore(score, ok, bestScore, bestOK) {
			best, bestScore, bestOK = t, score, ok
		}
	}
	return best, nil
}
