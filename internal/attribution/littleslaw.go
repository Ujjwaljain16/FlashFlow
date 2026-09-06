// Package attribution generalizes the queueing-theoretic checks this
// project has run by hand since 006-D/006-E into a reusable module --
// the PRD's "automated queueing-theoretic attribution engine" (§6.4,
// TRD §14), missing per the Stage 8 audit's F-08. It is deliberately
// its own package, not folded into internal/statistics: that package's
// own doc comment insists on zero domain knowledge (percentiles,
// Mann-Whitney, Cliff's Delta, bootstrap CIs all apply to any numeric
// sample), while Little's Law and utilization are specifically
// queueing-theory concepts tied to FlashFlow's own domain (targets,
// service times, arrival rates).
package attribution

import "fmt"

// Sample is one (L, lambda, W) measurement to check against Little's
// Law (L = lambda * W): L is the time-averaged number of requests in
// the system, Lambda is the arrival/throughput rate in requests per
// second, and W is the mean time each request spends in the system, in
// SECONDS (matching Lambda's per-second unit -- a caller measuring
// latency in milliseconds must divide by 1000 before constructing a
// Sample, the same conversion 006-D's own inline math already made
// explicit).
type Sample struct {
	L      float64
	Lambda float64
	W      float64
}

// ErrorMetrics reports how well Little's Law's prediction (Lambda*W)
// matched an independently-measured L. RelError follows 006-D's own
// sign convention: (Predicted-Observed)/Observed, so a positive value
// means the law's prediction overshot the measured L.
type ErrorMetrics struct {
	Predicted float64 // Lambda * W
	Observed  float64 // L
	AbsError  float64
	RelError  float64 // 0 when Observed is exactly 0 (avoids a division by zero; only meaningful metric left in that degenerate case is AbsError)
}

// CheckLittlesLaw generalizes 006-D's exact check (L ~= lambda*W) to any
// Sample. It is a pure consistency check, not a queueing-behavior
// simulator: it takes L, Lambda, and W as already-measured inputs and
// reports how closely they satisfy the law, the same computation 006-D
// performed inline before this package existed.
func CheckLittlesLaw(s Sample) (ErrorMetrics, error) {
	if s.Lambda < 0 {
		return ErrorMetrics{}, fmt.Errorf("attribution: Lambda must be non-negative, got %v", s.Lambda)
	}
	if s.W < 0 {
		return ErrorMetrics{}, fmt.Errorf("attribution: W must be non-negative, got %v", s.W)
	}
	if s.L < 0 {
		return ErrorMetrics{}, fmt.Errorf("attribution: L must be non-negative, got %v", s.L)
	}
	predicted := s.Lambda * s.W
	absErr := predicted - s.L
	relErr := 0.0
	if s.L != 0 {
		relErr = absErr / s.L
	}
	return ErrorMetrics{Predicted: predicted, Observed: s.L, AbsError: absErr, RelError: relErr}, nil
}
