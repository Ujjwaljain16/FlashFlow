package replay

import (
	"math/rand"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
)

// trackerInstrumentation adapts the project's existing LoadTracker /
// LatencyTracker to Instrumentation. Either field may be nil, for a
// policy that only needs one of the two.
type trackerInstrumentation struct {
	load *proxy.LoadTracker
	lat  *proxy.LatencyTracker
}

func (t trackerInstrumentation) OnDispatch(target string) {
	if t.load != nil {
		t.load.Increment(target)
	}
}

func (t trackerInstrumentation) OnComplete(target string, latency time.Duration) {
	if t.load != nil {
		t.load.Decrement(target)
	}
	if t.lat != nil {
		t.lat.Observe(target, latency)
	}
}

// The following PolicySpec constructors mirror exactly how each policy
// has been built in every experiment since Stage 3/7 (see e.g.
// cmd/experiment-007b/main.go) -- RunWorld's contract requires each New
// call to build entirely fresh trackers and a fresh selector, which is
// what every one of these closures does.

// RoundRobinPolicy needs no endogenous trackers at all.
func RoundRobinPolicy() PolicySpec {
	return PolicySpec{
		Name: "round-robin",
		New: func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation) {
			return proxy.NewRoundRobinSelector(), NoInstrumentation{}
		},
	}
}

// LeastConnectionsPolicy tracks in-flight load only.
func LeastConnectionsPolicy() PolicySpec {
	return PolicySpec{
		Name: "least-connections",
		New: func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			return proxy.NewLeastConnectionsSelector(load), trackerInstrumentation{load: load}
		},
	}
}

// EWMAPolicy tracks smoothed latency only.
func EWMAPolicy() PolicySpec {
	return PolicySpec{
		Name: "ewma",
		New: func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation) {
			lat := proxy.NewLatencyTracker(0.2)
			return proxy.NewEWMASelector(lat), trackerInstrumentation{lat: lat}
		},
	}
}

// P2CLoadPolicy tracks in-flight load and needs the Scenario's Seed for
// its random pair sampling -- the reason PolicySpec.New is threaded a
// seed at all, rather than each policy picking its own.
func P2CLoadPolicy() PolicySpec {
	return PolicySpec{
		Name: "p2c-load",
		New: func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			rng := rand.New(rand.NewSource(seed))
			return proxy.NewP2CSelector(proxy.ScorerFromLoad(load), rng), trackerInstrumentation{load: load}
		},
	}
}

// AdaptivePolicy tracks both load and latency, and additionally reads
// the World's own clock for staleness -- the one policy that uses the
// clk parameter.
func AdaptivePolicy() PolicySpec {
	return PolicySpec{
		Name: "adaptive",
		New: func(clk clock.Clock, seed int64) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			lat := proxy.NewLatencyTracker(0.2)
			sel := proxy.NewAdaptiveSelector(load, lat, nil, nil, clk, proxy.DefaultAdaptiveConfig())
			return sel, trackerInstrumentation{load: load, lat: lat}
		},
	}
}
