package challenge

import (
	"math/rand"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"flashflow/internal/netsim"
)

// countingRoundTripper counts how many requests actually reach it --
// the way to confirm a dropped request in netsim.Transport genuinely
// never calls the underlying transport, not merely that it returns an
// error.
type countingRoundTripper struct {
	calls atomic.Uint64
}

func (c *countingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
}

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/x", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	return req
}

// TestNetworkChallenge_RecoveryFromLossyToClean confirms a link's
// Conditions can change mid-run -- from total loss to a clean link --
// and that the Transport correctly reflects the NEW conditions
// immediately, not some stale cached behavior from before the change.
// This is the "recovery" case existing netsim tests (loss=0 always,
// loss=1 always, independently) don't cover: a single link transitioning
// between the two.
func TestNetworkChallenge_RecoveryFromLossyToClean(t *testing.T) {
	base := &countingRoundTripper{}
	rng := rand.New(rand.NewSource(1))
	transport := netsim.NewTransport(base, netsim.Conditions{LossRate: 1.0}, rng)

	const n = 20
	for i := 0; i < n; i++ {
		if _, err := transport.RoundTrip(newRequest(t)); err == nil {
			t.Fatalf("request %d: expected simulated loss while LossRate=1.0", i)
		}
	}
	if base.calls.Load() != 0 {
		t.Fatalf("expected 0 calls to reach the base transport during total loss, got %d", base.calls.Load())
	}

	// The link recovers.
	transport.Conditions = netsim.Conditions{LossRate: 0}
	for i := 0; i < n; i++ {
		if _, err := transport.RoundTrip(newRequest(t)); err != nil {
			t.Fatalf("request %d: expected no loss after recovery, got %v", i, err)
		}
	}
	if base.calls.Load() != n {
		t.Fatalf("expected all %d post-recovery requests to reach the base transport, got %d", n, base.calls.Load())
	}

	stats := transport.Snapshot()
	if stats.Dropped != n {
		t.Fatalf("expected exactly %d dropped requests (the pre-recovery half), got %d", n, stats.Dropped)
	}
	if stats.Requests != 2*n {
		t.Fatalf("expected %d total requests recorded, got %d", 2*n, stats.Requests)
	}
}

// TestNetworkChallenge_CombinedJitterAndLoss confirms latency/jitter and
// loss apply TOGETHER correctly, not just each in isolation (which
// existing netsim tests already cover separately): a dropped request
// must never reach the base transport regardless of jitter, and a
// successful request's delay must stay within the configured
// Latency+/-Jitter envelope.
func TestNetworkChallenge_CombinedJitterAndLoss(t *testing.T) {
	base := &countingRoundTripper{}
	rng := rand.New(rand.NewSource(7))
	cond := netsim.Conditions{Latency: 30 * time.Millisecond, Jitter: 10 * time.Millisecond, LossRate: 0.5}
	transport := netsim.NewTransport(base, cond, rng)

	const n = 50 // enough draws at LossRate=0.5 to almost certainly hit both outcomes, without this real-timer test taking many seconds
	dropped, succeeded := 0, 0
	for i := 0; i < n; i++ {
		start := time.Now()
		_, err := transport.RoundTrip(newRequest(t))
		elapsed := time.Since(start)
		if err != nil {
			dropped++
			continue
		}
		succeeded++
		// Envelope: [Latency-Jitter, Latency+Jitter], with generous
		// scheduling slack for the real wall-clock sleep this transport
		// actually performs -- this is genuinely real-timer-based (not
		// virtual time), so occasional tens-of-ms OS scheduling jitter
		// is expected and not itself a netsim defect; the slack exists
		// to absorb that without making the envelope check meaningless.
		min := cond.Latency - cond.Jitter
		max := cond.Latency + cond.Jitter + 75*time.Millisecond // slack
		if elapsed < min {
			t.Fatalf("request %d: elapsed %v under the minimum envelope %v", i, elapsed, min)
		}
		if elapsed > max {
			t.Fatalf("request %d: elapsed %v over the maximum envelope %v (possible scheduling issue, investigate before dismissing)", i, elapsed, max)
		}
	}

	if uint64(succeeded) != base.calls.Load() {
		t.Fatalf("expected every successful RoundTrip to reach the base transport exactly once: succeeded=%d base.calls=%d", succeeded, base.calls.Load())
	}
	// With LossRate=0.5 over 200 draws, both outcomes must occur --
	// this is a sanity bound on the seeded RNG actually exercising both
	// paths, not a precise statistical claim.
	if dropped == 0 || succeeded == 0 {
		t.Fatalf("expected both dropped and successful requests at LossRate=0.5 over %d draws, got dropped=%d succeeded=%d", n, dropped, succeeded)
	}
}
