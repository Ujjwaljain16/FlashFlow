package proxy

import (
	"errors"
	"net/http"
	"sync/atomic"
)

var (
	// ErrNoHealthyTargets is returned when all available upstreams fail health checks.
	ErrNoHealthyTargets = errors.New("no healthy targets available")
)

// TargetSelector defines the pluggable interface for upstream selection.
// In Stage 2, this is static or simple deterministic selection.
// In Stage 3, this will be replaced by dynamic routing policies (RR, LeastConns, EWMA, P2C).
type TargetSelector interface {
	SelectTarget(r *http.Request, availableTargets []string) (string, error)
}

// StaticSelector always selects the first available target or a fixed target.
type StaticSelector struct {
	fixedTarget string
}

// NewStaticSelector creates a selector that always targets a specific endpoint.
func NewStaticSelector(fixedTarget string) *StaticSelector {
	return &StaticSelector{fixedTarget: fixedTarget}
}

// SelectTarget returns the fixed target if available, or the first available target.
func (s *StaticSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}
	if s.fixedTarget != "" {
		for _, t := range available {
			if t == s.fixedTarget {
				return t, nil
			}
		}
	}
	return available[0], nil
}

// RoundRobinSelector deterministically cycles through available targets.
type RoundRobinSelector struct {
	counter atomic.Uint64
}

// NewRoundRobinSelector creates a round-robin selector across available targets.
func NewRoundRobinSelector() *RoundRobinSelector {
	return &RoundRobinSelector{}
}

// SelectTarget selects the next healthy target using round-robin indexing.
func (s *RoundRobinSelector) SelectTarget(r *http.Request, available []string) (string, error) {
	if len(available) == 0 {
		return "", ErrNoHealthyTargets
	}
	idx := s.counter.Add(1) - 1
	return available[idx%uint64(len(available))], nil
}
