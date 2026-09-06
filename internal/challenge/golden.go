// Package challenge is FlashFlow's permanent regression suite: a fixed
// golden scenario with known invariants, plus adversarial cases across
// routing, health, replay, and tuning specifically built to break the
// system (master context rules 38-39, 63). It is an ordinary Go test
// package, deliberately -- "every future code change should be able to
// rerun golden scenarios + challenge scenarios + determinism checks"
// needs no new tooling when `go test ./...` (already this project's
// standing quality gate) already runs it.
//
// Cache (internal/cache) and network-impairment (internal/netsim)
// challenge cases are NOT duplicated here: both packages already carry
// their own permanent unit tests proving the specific properties the
// master context's example categories ask for (a cache hit never
// reaching the upstream, TTL expiry, loss/latency simulation), and
// those tests already run on every `go test ./...` invocation. Stage
// 8's job is to confirm that coverage exists and is part of the whole
// suite, not to re-implement it inside a new package.
package challenge

import (
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

// GoldenScenario is the one, permanent, fixed-manifest scenario master
// context rule 38 asks for: 3 heterogeneous targets, one mid-run health
// degradation, and hot/cold cache-key rotation exercising
// AdaptiveSelector's Cache signal -- exactly the shape rule 38's own
// example describes. Its exact numeric outputs (final metrics) are
// deliberately NOT asserted anywhere in this package: rule 38 explicitly
// warns against hardcoding approximate benchmark numbers as correctness
// conditions. What IS asserted, in golden_test.go, are structural
// invariants that must hold regardless of the exact routing outcome:
// no unhealthy target is ever selected, the trace is deterministic
// across reruns, and counterfactual worlds evaluated against it remain
// isolated from each other.
func GoldenScenario() replay.Scenario {
	const requests = 300
	const spacing = 5 * time.Millisecond
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: goldenKeyFor(i)}
	}

	return replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "edge-a", ServiceTime: 30 * time.Millisecond},
			{Name: "edge-b", ServiceTime: 80 * time.Millisecond},
			{Name: "edge-c", ServiceTime: 50 * time.Millisecond},
		},
		Arrivals: arrivals,
		Failures: []replay.FailureWindow{
			// edge-b: a mid-run degradation, well clear of both the
			// start and end of the traffic window, so the scenario
			// exercises both "adapt to a failure" and "adapt to a
			// recovery" within one run.
			{Target: "edge-b", DownAt: clock.VirtualTime(500 * time.Millisecond), UpAt: clock.VirtualTime(1200 * time.Millisecond)},
		},
		Horizon: clock.VirtualTime(2 * time.Second),
		Seeds:   replay.DeriveSeeds(42),
	}
}

// goldenKeyFor mirrors the hot/cold rotation pattern used throughout
// Stage 7's experiments (007-B onward): a dominant "hot" key plus a
// small rotating set of "cold" ones, enough for AdaptiveSelector's
// Cache signal to have something to be right or wrong about.
func goldenKeyFor(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	switch i % 6 {
	case 1:
		return "/cold-1"
	case 3:
		return "/cold-2"
	default:
		return "/cold-3"
	}
}
