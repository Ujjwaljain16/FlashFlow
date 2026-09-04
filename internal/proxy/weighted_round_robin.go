package proxy

import (
	"net/http"
	"sync"
)

// TargetWeights maps a target (a target URL string) to its configured
// relative capacity weight.
type TargetWeights map[string]int

type wrrState struct {
	effectiveWeight int
	currentWeight   int
}

// WeightedRoundRobinSelector distributes selections proportional to
// statically configured weights, using nginx/LVS's smooth weighted
// round-robin algorithm (add each target's weight to a running counter,
// pick the max, subtract the total from the winner) rather than naive
// sequence expansion (e.g. [A,A,A,A,B]). Naive expansion is bursty — a
// heavily weighted target gets several concurrent requests back-to-back
// instead of interleaved — which defeats the point of a capacity weight.
//
// Per-target state persists across calls, including while a target is
// temporarily excluded from `available` (e.g. UNHEALTHY): its counter
// just stops accumulating and resumes where it left off, rather than
// resetting — routing doesn't need to know *why* a target disappeared,
// that's health's job (internal/health).
type WeightedRoundRobinSelector struct {
	mu            sync.Mutex
	weights       TargetWeights
	defaultWeight int
	state         map[string]*wrrState
}

// NewWeightedRoundRobinSelector creates a WRR selector from static target
// weights. A missing or non-positive weight defaults to 1 rather than
// excluding the target — exclusion is health's decision, not the
// weighting scheme's.
func NewWeightedRoundRobinSelector(weights TargetWeights) *WeightedRoundRobinSelector {
	w := make(TargetWeights, len(weights))
	for k, v := range weights {
		w[k] = v
	}
	return &WeightedRoundRobinSelector{
		weights:       w,
		defaultWeight: 1,
		state:         make(map[string]*wrrState),
	}
}

func (s *WeightedRoundRobinSelector) weightFor(target string) int {
	if w, ok := s.weights[target]; ok && w > 0 {
		return w
	}
	return s.defaultWeight
}

// SelectTarget implements TargetSelector.
func (s *WeightedRoundRobinSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, t := range available {
		st, ok := s.state[t]
		if !ok {
			st = &wrrState{effectiveWeight: s.weightFor(t)}
			s.state[t] = st
		}
		st.currentWeight += st.effectiveWeight
		total += st.effectiveWeight
	}

	var best string
	var bestWeight int
	first := true
	for _, t := range available {
		st := s.state[t]
		if first || st.currentWeight > bestWeight {
			best = t
			bestWeight = st.currentWeight
			first = false
		}
	}

	s.state[best].currentWeight -= total
	return best, nil
}
