package proxy

import (
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These benchmarks measure the pure per-call cost of a routing
// decision -- SelectTarget alone, with all trackers pre-warmed outside
// the timed region -- the "decision cost" master context rule 51 asks
// the routing layer to report, isolated from network I/O, event-loop
// overhead, or anything else in the request path.

var benchTargets = []string{"a", "b", "c"}
var benchRequest = httptest.NewRequest(http.MethodGet, "/bench", nil)

func BenchmarkRoundRobinSelector_SelectTarget(b *testing.B) {
	sel := NewRoundRobinSelector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWeightedRoundRobinSelector_SelectTarget(b *testing.B) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 3, "b": 2, "c": 1})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLeastConnectionsSelector_SelectTarget(b *testing.B) {
	load := NewLoadTracker()
	sel := NewLeastConnectionsSelector(load)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEWMASelector_SelectTarget(b *testing.B) {
	lat := NewLatencyTracker(0.2)
	for _, t := range benchTargets {
		lat.Observe(t, 20*time.Millisecond) // pre-warm past cold-start, outside the timed region
	}
	sel := NewEWMASelector(lat)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkP2CSelector_SelectTarget(b *testing.B) {
	load := NewLoadTracker()
	sel := NewP2CSelector(ScorerFromLoad(load), rand.New(rand.NewSource(1)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAdaptiveSelector_SelectTarget(b *testing.B) {
	load := NewLoadTracker()
	lat := NewLatencyTracker(0.2)
	for _, t := range benchTargets {
		lat.Observe(t, 20*time.Millisecond) // pre-warm past cold-start
	}
	sel := NewAdaptiveSelector(load, lat, nil, nil, nil, DefaultAdaptiveConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sel.SelectTarget(benchRequest, benchTargets); err != nil {
			b.Fatal(err)
		}
	}
}
