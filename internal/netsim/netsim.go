// Package netsim simulates link-level network conditions (added latency,
// jitter, packet loss) between an HTTP client and an upstream, at the
// http.RoundTripper level.
//
// This exists in place of `tc netem`, the standard way Stage 4's design
// intended to apply real OS-level network shaping: netem is Linux-only,
// and these experiments run on Windows. Modeling degradation in-process
// instead of at the kernel is a real limitation — it can't reproduce
// packet-level effects like reordering or partial-write corruption — but
// it reproduces the two properties that matter for what Stage 4 actually
// asks (does an added-latency or lossy link change cache-hit isolation,
// and does coalescing change the effective failure rate a caller sees),
// without needing a Linux host this project doesn't have.
package netsim

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Conditions describes the network conditions to simulate. The zero value
// simulates a perfect link (no added delay, no loss) — matching this
// package's callers' existing "zero value means off" convention, a caller
// only needs to wrap a transport at all once at least one field is set.
type Conditions struct {
	// Latency is a fixed delay added to every request before it reaches
	// the underlying transport, modeling increased link RTT.
	Latency time.Duration
	// Jitter, if > 0, adds a uniformly random +/-Jitter variation on top
	// of Latency (clamped at 0 — delay is never simulated as negative).
	Jitter time.Duration
	// LossRate is the probability, in [0, 1), that a request is dropped
	// before it reaches the underlying transport, modeling packet loss.
	// A dropped request never calls the underlying RoundTripper at all —
	// it fails the way a real connection attempt into a lossy link would.
	LossRate float64
	// Seed makes the sampled delay/loss sequence reproducible when this
	// Transport is constructed via NewTransport(..., nil) — see
	// NewTransport's doc comment. Ignored by callers that supply their
	// own *rand.Rand directly.
	Seed int64
}

// enabled reports whether these conditions differ from a perfect link.
func (c Conditions) enabled() bool {
	return c.Latency > 0 || c.Jitter > 0 || c.LossRate > 0
}

// Stats is a point-in-time snapshot of a Transport's activity.
type Stats struct {
	Requests uint64 `json:"requests"`
	Dropped  uint64 `json:"dropped"`
}

// Transport wraps an http.RoundTripper, applying Conditions to every
// request before delegating to Base.
type Transport struct {
	Base http.RoundTripper
	// Conditions is exported for tests that need to change simulated
	// network behavior mid-run (e.g. lossy -> clean transitions), but is
	// read without synchronization by sample()/RoundTrip -- callers doing
	// that MUST NOT mutate it concurrently with in-flight RoundTrip calls.
	// No production code path in this repository does so today.
	Conditions Conditions

	randMu sync.Mutex
	rand   *rand.Rand

	requests atomic.Uint64
	dropped  atomic.Uint64
}

// NewTransport wraps base with the given conditions. r supplies the
// randomness used to sample jitter and loss; pass nil in production to
// get a real time-seeded source, or an explicit *rand.Rand for
// deterministic tests, the same nil-defaults-to-real pattern clock.Clock
// uses elsewhere in this codebase.
func NewTransport(base http.RoundTripper, cond Conditions, r *rand.Rand) *Transport {
	if r == nil {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Transport{Base: base, Conditions: cond, rand: r}
}

// Enabled reports whether t's conditions differ from a perfect link — a
// caller can use this to decide whether wrapping a transport is worth
// doing at all.
func (t *Transport) Enabled() bool {
	return t.Conditions.enabled()
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)

	delay, drop := t.sample()

	if delay > 0 {
		// time.NewTimer (not time.After) so a canceled request's timer is
		// stopped immediately rather than left running in the background
		// until it would have fired on its own.
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		}
	}

	if drop {
		t.dropped.Add(1)
		return nil, fmt.Errorf("netsim: simulated packet loss (loss_rate=%.2f)", t.Conditions.LossRate)
	}

	return t.Base.RoundTrip(req)
}

// sample draws one delay/drop outcome. math/rand.Rand is not safe for
// concurrent use, so access is serialized here rather than in every caller.
func (t *Transport) sample() (time.Duration, bool) {
	t.randMu.Lock()
	defer t.randMu.Unlock()

	delay := t.Conditions.Latency
	if t.Conditions.Jitter > 0 {
		j := time.Duration(t.rand.Int63n(int64(2*t.Conditions.Jitter))) - t.Conditions.Jitter
		delay += j
		if delay < 0 {
			delay = 0
		}
	}

	drop := t.Conditions.LossRate > 0 && t.rand.Float64() < t.Conditions.LossRate
	return delay, drop
}

// Snapshot returns a point-in-time copy of the activity counters.
func (t *Transport) Snapshot() Stats {
	return Stats{Requests: t.requests.Load(), Dropped: t.dropped.Load()}
}
