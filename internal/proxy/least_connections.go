package proxy

import "net/http"

// LeastConnectionsSelector picks the available target with the lowest
// current in-flight count from a shared LoadTracker. Read-only — it never
// mutates the tracker (see LoadTracker.Get for the read/increment race
// this implies).
type LeastConnectionsSelector struct {
	tracker *LoadTracker
}

// NewLeastConnectionsSelector's tracker must be the same *LoadTracker
// instance the proxy updates (ReverseProxy.LoadTracker(), attached via
// SetSelector) — a different instance would never see real traffic.
func NewLeastConnectionsSelector(tracker *LoadTracker) *LeastConnectionsSelector {
	return &LeastConnectionsSelector{tracker: tracker}
}

// SelectTarget implements TargetSelector. Ties (including the common case
// where every candidate is at load 0) are broken by position in `available`,
// for determinism.
func (s *LeastConnectionsSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}

	best := available[0]
	bestLoad := s.tracker.Get(best)
	for _, t := range available[1:] {
		load := s.tracker.Get(t)
		if load < bestLoad {
			best = t
			bestLoad = load
		}
	}
	return best, nil
}
