package health

import (
	"sync"

	"flashflow/internal/clock"
)

// State represents the 4-state health lifecycle of a target edge.
type State string

const (
	StateHealthy    State = "HEALTHY"
	StateDegraded   State = "DEGRADED"
	StateUnhealthy  State = "UNHEALTHY"
	StateRecovering State = "RECOVERING"
)

// TargetHealth tracks the health status, counters, and history for a single target.
type TargetHealth struct {
	Target            string            `json:"target"`
	State             State             `json:"state"`
	ConsecutiveFails  int               `json:"consecutive_fails"`
	ConsecutivePasses int               `json:"consecutive_passes"`
	TotalAppRequests  uint64            `json:"total_app_requests"`
	TotalAppErrors    uint64            `json:"total_app_errors"`
	LastCheck         clock.VirtualTime `json:"last_check"`
	LastStateChange   clock.VirtualTime `json:"last_state_change"`
}

// Config defines thresholds for health state transitions.
//
// DegradedErrorRate is evaluated against cumulative lifetime stats
// (TotalAppErrors/TotalAppRequests since registration), not a rolling
// window — deliberately simple for Stage 2, but it means a target can
// stay effectively degraded long after the underlying problem clears,
// since old errors dilute slowly. Rolling-window rate is deferred.
type Config struct {
	UnhealthyFailThreshold int     `json:"unhealthy_fail_threshold"`  // Probe failures to become UNHEALTHY (default 2)
	RecoveryPassThreshold  int     `json:"recovery_pass_threshold"`   // Probe successes to become HEALTHY from RECOVERING (default 2)
	DegradedErrorRate      float64 `json:"degraded_error_rate"`       // Cumulative lifetime 5xx error rate threshold (default 0.20)
	MinAppRequestsForRate  uint64  `json:"min_app_requests_for_rate"` // Min requests before evaluating error rate (default 10)
}

// DefaultConfig returns reasonable default thresholds for Stage 2.
func DefaultConfig() Config {
	return Config{
		UnhealthyFailThreshold: 2,
		RecoveryPassThreshold:  2,
		DegradedErrorRate:      0.20,
		MinAppRequestsForRate:  10,
	}
}

// Registry manages the health states of all known upstream edge targets.
type Registry struct {
	mu      sync.RWMutex
	clock   clock.Clock
	config  Config
	targets map[string]*TargetHealth
}

// NewRegistry creates a new health registry.
func NewRegistry(clk clock.Clock, cfg Config) *Registry {
	if clk == nil {
		clk = clock.NewWallClock()
	}
	return &Registry{
		clock:   clk,
		config:  cfg,
		targets: make(map[string]*TargetHealth),
	}
}

// RegisterTarget adds a target with initial HEALTHY state.
func (r *Registry) RegisterTarget(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.targets[target]; !exists {
		now := r.clock.Now()
		r.targets[target] = &TargetHealth{
			Target:          target,
			State:           StateHealthy,
			LastCheck:       now,
			LastStateChange: now,
		}
	}
}

// RecordProbeResult records the outcome of an active /health probe.
func (r *Registry) RecordProbeResult(target string, success bool) State {
	r.mu.Lock()
	defer r.mu.Unlock()

	th, exists := r.targets[target]
	if !exists {
		now := r.clock.Now()
		th = &TargetHealth{
			Target:          target,
			State:           StateHealthy,
			LastCheck:       now,
			LastStateChange: now,
		}
		r.targets[target] = th
	}

	now := r.clock.Now()
	th.LastCheck = now

	if success {
		th.ConsecutivePasses++
		th.ConsecutiveFails = 0

		switch th.State {
		case StateUnhealthy:
			th.State = StateRecovering
			th.LastStateChange = now
			if th.ConsecutivePasses >= r.config.RecoveryPassThreshold {
				th.State = StateHealthy
			}
		case StateRecovering:
			if th.ConsecutivePasses >= r.config.RecoveryPassThreshold {
				th.State = StateHealthy
				th.LastStateChange = now
			}
		case StateDegraded:
			// If probe succeeds and app error rate is not elevated, restore to Healthy
			if th.TotalAppRequests >= r.config.MinAppRequestsForRate {
				errRate := float64(th.TotalAppErrors) / float64(th.TotalAppRequests)
				if errRate < r.config.DegradedErrorRate {
					th.State = StateHealthy
					th.LastStateChange = now
				}
			}
		case StateHealthy:
			// Remains healthy
		}
	} else {
		th.ConsecutiveFails++
		th.ConsecutivePasses = 0

		switch th.State {
		case StateHealthy, StateDegraded, StateRecovering:
			if th.ConsecutiveFails >= r.config.UnhealthyFailThreshold {
				th.State = StateUnhealthy
				th.LastStateChange = now
			}
		case StateUnhealthy:
			// Remains unhealthy
		}
	}

	return th.State
}

// RecordAppResult records live application traffic status codes (e.g. 502/503/500).
//
// TotalAppRequests/TotalAppErrors accumulate for the lifetime of the target
// (see Config doc comment) — this is not a rolling window.
func (r *Registry) RecordAppResult(target string, statusCode int) State {
	r.mu.Lock()
	defer r.mu.Unlock()

	th, exists := r.targets[target]
	if !exists {
		return StateHealthy
	}

	now := r.clock.Now()
	th.TotalAppRequests++
	if statusCode >= 500 {
		th.TotalAppErrors++
	}

	// Evaluate application error rate if we have enough sample volume
	if th.TotalAppRequests >= r.config.MinAppRequestsForRate {
		errRate := float64(th.TotalAppErrors) / float64(th.TotalAppRequests)
		if errRate >= r.config.DegradedErrorRate && th.State == StateHealthy {
			th.State = StateDegraded
			th.LastStateChange = now
		}
	}

	return th.State
}

// IsAvailable returns true if the target is in a state eligible to receive traffic.
func (r *Registry) IsAvailable(target string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	th, exists := r.targets[target]
	if !exists {
		return true // Default optimistic if unknown
	}
	return th.State == StateHealthy || th.State == StateDegraded
}

// GetHealth returns a copy of the health stats for a target.
func (r *Registry) GetHealth(target string) (TargetHealth, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	th, exists := r.targets[target]
	if !exists {
		return TargetHealth{}, false
	}
	return *th, true
}

// Snapshot returns all current target states.
func (r *Registry) Snapshot() map[string]TargetHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make(map[string]TargetHealth, len(r.targets))
	for k, v := range r.targets {
		out[k] = *v
	}
	return out
}
