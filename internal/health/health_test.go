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

// TestRegistry_RecordAppResult_AutoRegistersLikeRecordProbeResult
// regression-tests F-35: RecordAppResult used to silently no-op and
// return StateHealthy for a target that hadn't been registered yet,
// while RecordProbeResult auto-registered on first observation --
// dropping app-level data for any caller that recorded a result before
// registration completed, an inconsistency with no test coverage.
func TestRegistry_RecordAppResult_AutoRegistersLikeRecordProbeResult(t *testing.T) {
	clk := &staticClock{t: 1000}
	reg := NewRegistry(clk, DefaultConfig())

	reg.RecordAppResult("never-registered", 500)

	h, ok := reg.GetHealth("never-registered")
	if !ok {
		t.Fatalf("expected RecordAppResult to auto-register the target")
	}
	if h.TotalAppRequests != 1 || h.TotalAppErrors != 1 {
		t.Fatalf("expected the recorded result to actually count, got %+v", h)
	}
}

// TestRegistry_Deregister_ResetsStateOnReRegister regression-tests F-36:
// there was no way to remove a target's health state, so a hypothetical
// remove-then-re-add of a target would have no reset primitive available
// at all.
func TestRegistry_Deregister_ResetsStateOnReRegister(t *testing.T) {
	clk := &staticClock{t: 1000}
	reg := NewRegistry(clk, Config{UnhealthyFailThreshold: 1})
	target := "flaky"
	reg.RegisterTarget(target)
	reg.RecordProbeResult(target, false) // now UNHEALTHY with ConsecutiveFails=1

	h, _ := reg.GetHealth(target)
	if h.State != StateUnhealthy {
		t.Fatalf("test setup failed: expected UNHEALTHY, got %s", h.State)
	}

	reg.Deregister(target)
	if _, ok := reg.GetHealth(target); ok {
		t.Fatalf("expected GetHealth to report absent after Deregister")
	}

	reg.RegisterTarget(target)
	h, ok := reg.GetHealth(target)
	if !ok {
		t.Fatalf("expected the target to be present after re-registering")
	}
	if h.State != StateHealthy || h.ConsecutiveFails != 0 {
		t.Fatalf("expected a fresh HEALTHY state after Deregister+RegisterTarget, got %+v", h)
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

// TestChecker_StartStopStart_OnlyOneActiveLoop regression-tests the goroutine
// leak where runLoop read c.stopCh as a struct field on every iteration
// instead of a value captured once at Start() time: a Stop() immediately
// followed by another Start() reassigned c.stopCh to a new, open channel
// before the old goroutine's next loop iteration re-evaluated it, so the old
// goroutine never observed its own stop signal and kept running forever
// alongside the new one -- doubling the probe rate against every target.
func TestChecker_StartStopStart_OnlyOneActiveLoop(t *testing.T) {
	var probes atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	clk := clock.NewWallClock()
	reg := NewRegistry(clk, Config{})
	const interval = 10 * time.Millisecond
	checker := NewChecker(reg, clk, CheckerConfig{Interval: interval, Timeout: 50 * time.Millisecond}, []string{ts.URL})

	checker.Start()
	time.Sleep(3 * interval) // let the first loop settle into its ticker wait

	// The exact race this test targets: Stop() then Start() back-to-back,
	// with no delay, so a leaked old loop would still be inside its select
	// when the new one starts.
	checker.Stop()
	checker.Start()
	defer checker.Stop()

	probes.Store(0)
	const window = 15 * interval
	time.Sleep(window)

	got := probes.Load()
	// A single healthy loop probes roughly window/interval times (15, plus
	// the one immediate probe Start's runLoop fires before its first
	// tick). A leaked second loop would roughly double this. Assert well
	// below 2x to give real scheduling jitter room without masking a leak.
	maxExpected := int64(window/interval) * 3 / 2
	if got > maxExpected {
		t.Fatalf("got %d probes in %v (interval %v) -- suggests more than one active runLoop; expected at most ~%d", got, window, interval, maxExpected)
	}
}
