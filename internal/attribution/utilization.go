package attribution

import (
	"fmt"
	"time"

	"flashflow/internal/replay"
)

// Utilization computes rho = lambda * serviceTime -- the fraction of
// one server's capacity a given arrival rate consumes, in the
// single-server (M/M/1-shaped) sense: rho=0.8 means the server is busy
// 80% of the time on average. lambda is requests/second; serviceTime
// converts to a per-request duration in seconds internally. A negative
// lambda or serviceTime is a caller error (fixed rate/duration inputs
// this project's own generators never produce), not a value this
// function tries to interpret -- callers construct these from measured
// or configured quantities that are non-negative by construction.
func Utilization(lambda float64, serviceTime time.Duration) float64 {
	return lambda * serviceTime.Seconds()
}

// UtilizationFromWorld computes each target's utilization from one
// replay.WorldResult: lambda_target = (completions routed to that
// target) / horizon, rho_target = Utilization(lambda_target,
// target.ServiceTime).
//
// Honesty this function's callers must preserve: rho here comes from
// exogenous ServiceTime and observed completion counts alone --
// RunWorld's virtual engine has no queueing or contention model (a
// request's latency is exactly its target's fixed ServiceTime, never
// inflated by other in-flight work), so this measures offered-load-
// versus-capacity in the idealized sense useful for a monotonicity
// check (see internal/challenge/metamorphic_test.go), not a claim that
// RunWorld models queueing delay the way the real engine's finite
// connection pool actually does.
func UtilizationFromWorld(result replay.WorldResult, targets []replay.TargetProfile, horizon time.Duration) (map[string]float64, error) {
	if horizon <= 0 {
		return nil, fmt.Errorf("attribution: horizon must be positive, got %v", horizon)
	}
	serviceTimes := make(map[string]time.Duration, len(targets))
	for _, t := range targets {
		serviceTimes[t.Name] = t.ServiceTime
	}

	out := make(map[string]float64, len(targets))
	for _, t := range targets {
		completed := result.CompletedByTarget[t.Name] // zero value (0) for a target with no completions -- a legitimate, not erroneous, result
		lambda := float64(completed) / horizon.Seconds()
		out[t.Name] = Utilization(lambda, serviceTimes[t.Name])
	}
	return out, nil
}
