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
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			return proxy.NewRoundRobinSelector(), NoInstrumentation{}
		},
	}
}

// WeightedRoundRobinPolicy assigns each target a static capacity weight
// inversely proportional to its ServiceTime -- the single most
// favorable case for WRR this project can construct: an operator who
// profiled every target's true performance perfectly, once, before
// startup. Even under that best case, the weights are frozen at
// RunWorld's start and never update again, which is exactly Stage 3's
// original finding about WRR (docs/learning/003-routing-policies.md):
// a configured weight encodes capacity but goes stale the moment
// reality changes (a mid-run failure, a load-based slowdown), while a
// live signal is self-correcting but blind to configured capacity.
// Weights are scaled to integers (TargetWeights is map[string]int)
// with a fixed multiplier, floored at 1 so no target is ever
// mistakenly assigned zero weight.
func WeightedRoundRobinPolicy() PolicySpec {
	return PolicySpec{
		Name: "weighted-round-robin",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			const scale = 1000.0
			weights := make(proxy.TargetWeights, len(targets))
			for _, t := range targets {
				ms := t.ServiceTime.Milliseconds()
				if ms < 1 {
					ms = 1
				}
				w := int(scale / float64(ms))
				if w < 1 {
					w = 1
				}
				weights[t.Name] = w
			}
			return proxy.NewWeightedRoundRobinSelector(weights), NoInstrumentation{}
		},
	}
}

// LeastConnectionsPolicy tracks in-flight load only.
func LeastConnectionsPolicy() PolicySpec {
	return PolicySpec{
		Name: "least-connections",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			return proxy.NewLeastConnectionsSelector(load), trackerInstrumentation{load: load}
		},
	}
}

// EWMAPolicy tracks smoothed latency only.
func EWMAPolicy() PolicySpec {
	return PolicySpec{
		Name: "ewma",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			lat := proxy.NewLatencyTracker(0.2)
			return proxy.NewEWMASelector(lat), trackerInstrumentation{lat: lat}
		},
	}
}

// P2CLoadPolicy tracks in-flight load and needs its own randomness for
// pair sampling, drawn from seeds.Policy -- the reason PolicySpec.New is
// threaded a SeedTree at all, rather than each policy picking its own.
func P2CLoadPolicy() PolicySpec {
	return PolicySpec{
		Name: "p2c-load",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			rng := rand.New(rand.NewSource(seeds.Policy))
			return proxy.NewP2CSelector(proxy.ScorerFromLoad(load), rng), trackerInstrumentation{load: load}
		},
	}
}

// AdaptivePolicy tracks both load and latency, and additionally reads
// the World's own clock for staleness -- the one policy that uses the
// clk parameter. Uses proxy.DefaultAdaptiveConfig(); for Stage 8's
// tuning, which evaluates many other AdaptiveConfig values, see
// AdaptivePolicyWithConfig.
func AdaptivePolicy() PolicySpec {
	return AdaptivePolicyWithConfig(proxy.DefaultAdaptiveConfig())
}

// AdaptivePolicyWithConfig is AdaptivePolicy parameterized by an
// arbitrary AdaptiveConfig -- the hook Stage 8's tuner uses to evaluate
// a candidate configuration through exactly the same RunWorld path
// every other policy comparison in this project already uses, rather
// than a second, tuner-specific evaluation mechanism.
func AdaptivePolicyWithConfig(cfg proxy.AdaptiveConfig) PolicySpec {
	return PolicySpec{
		Name: "adaptive",
		New: func(clk clock.Clock, seeds SeedTree, targets []TargetProfile) (proxy.TargetSelector, Instrumentation) {
			load := proxy.NewLoadTracker()
			lat := proxy.NewLatencyTracker(0.2)
			sel := proxy.NewAdaptiveSelector(load, lat, nil, nil, clk, cfg)
			return sel, trackerInstrumentation{load: load, lat: lat}
		},
	}
}
