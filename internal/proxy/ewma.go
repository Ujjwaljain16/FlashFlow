package proxy

import (
	"net/http"
	"time"
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
	for _, t := range available[1:] {
		latency, ok := s.tracker.Estimate(t)
		if preferCandidate(latency, ok, bestLatency, bestOK) {
			best, bestLatency, bestOK = t, latency, ok
		}
	}
	return best, nil
}

// preferCandidate: unobserved beats observed unconditionally; between two
// candidates in the same observed/unobserved state, lower latency wins and
// ties keep the current best.
func preferCandidate(latency time.Duration, ok bool, bestLatency time.Duration, bestOK bool) bool {
	if ok != bestOK {
		return !ok
	}
	if !ok {
		return false
	}
	return latency < bestLatency
}
