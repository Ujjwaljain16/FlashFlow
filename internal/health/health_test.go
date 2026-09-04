package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"flashflow/internal/clock"
)

type staticClock struct {
	t clock.VirtualTime
}

func (s *staticClock) Now() clock.VirtualTime {
	return s.t
}

func TestHealthRegistry_Transitions(t *testing.T) {
	clk := &staticClock{t: 1000}
	cfg := Config{
		UnhealthyFailThreshold: 2,
		RecoveryPassThreshold:  2,
		DegradedErrorRate:      0.25,
		MinAppRequestsForRate:  4,
	}

	reg := NewRegistry(clk, cfg)
	target := "edge-1:8001"
	reg.RegisterTarget(target)

	// 1. Initial state is HEALTHY
	h, ok := reg.GetHealth(target)
	if !ok || h.State != StateHealthy {
		t.Fatalf("expected initial state HEALTHY, got %s", h.State)
	}
	if !reg.IsAvailable(target) {
		t.Fatalf("expected target to be available")
	}

	// 2. Single probe failure -> stays HEALTHY (threshold is 2)
	st := reg.RecordProbeResult(target, false)
	if st != StateHealthy {
		t.Fatalf("expected state to stay HEALTHY after 1 failure, got %s", st)
	}

	// 3. Second probe failure -> transitions to UNHEALTHY
	st = reg.RecordProbeResult(target, false)
	if st != StateUnhealthy {
		t.Fatalf("expected state UNHEALTHY after 2 failures, got %s", st)
	}
	if reg.IsAvailable(target) {
		t.Fatalf("expected target to NOT be available when UNHEALTHY")
	}

	// 4. First probe success -> transitions to RECOVERING
	st = reg.RecordProbeResult(target, true)
	if st != StateRecovering {
		t.Fatalf("expected state RECOVERING after 1 success, got %s", st)
	}

	// 5. Second probe success -> transitions back to HEALTHY
	st = reg.RecordProbeResult(target, true)
	if st != StateHealthy {
		t.Fatalf("expected state HEALTHY after 2 recovery successes, got %s", st)
	}
	if !reg.IsAvailable(target) {
		t.Fatalf("expected target to be available when HEALTHY")
	}

	// 6. Application error rate threshold: 4 requests with 2 errors (50% > 25%) -> DEGRADED
	reg.RecordAppResult(target, http.StatusOK)
	reg.RecordAppResult(target, http.StatusOK)
	reg.RecordAppResult(target, http.StatusBadGateway)
	st = reg.RecordAppResult(target, http.StatusServiceUnavailable)
	if st != StateDegraded {
		t.Fatalf("expected state DEGRADED due to 50%% error rate, got %s", st)
	}
	// Target is still available when DEGRADED
	if !reg.IsAvailable(target) {
		t.Fatalf("expected target to remain available when DEGRADED")
	}
}

func TestChecker_ProbeLoop(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if healthy.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	clk := clock.NewWallClock()
	cfg := Config{
		UnhealthyFailThreshold: 1,
		RecoveryPassThreshold:  1,
	}
	reg := NewRegistry(clk, cfg)

	chkCfg := CheckerConfig{
		Interval: 50 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
		Path:     "/health",
	}
	checker := NewChecker(reg, clk, chkCfg, []string{ts.URL})

	// Initial probe
	checker.ProbeOnce(context.Background())
	if !reg.IsAvailable(ts.URL) {
		t.Fatalf("expected ts to be available initially")
	}

	// Flip to unhealthy
	healthy.Store(false)
	checker.ProbeOnce(context.Background())
	if reg.IsAvailable(ts.URL) {
		t.Fatalf("expected ts to become unavailable after probe failure")
	}

	// Flip back to healthy
	healthy.Store(true)
	checker.ProbeOnce(context.Background())
	if !reg.IsAvailable(ts.URL) {
		t.Fatalf("expected ts to recover to available after probe success")
	}
}
