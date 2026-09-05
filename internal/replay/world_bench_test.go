package replay

import (
	"testing"
	"time"

	"flashflow/internal/clock"
)

// BenchmarkRunWorld_AdaptivePolicy measures the whole virtual-engine
// pipeline's throughput -- scenario construction excluded, timed region
// covers exactly what one RunWorld call does (event scheduling,
// routing decisions, tracker updates, trace recording) -- reported as
// virtual requests/sec, the composite cost every experiment in this
// project actually pays per call, not a decomposed micro-benchmark.
func BenchmarkRunWorld_AdaptivePolicy(b *testing.B) {
	scenario := benchScenario(300)
	spec := AdaptivePolicy()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RunWorld(scenario, spec); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*300)/b.Elapsed().Seconds(), "virtual-requests/sec")
}

func BenchmarkRunWorld_RoundRobinPolicy(b *testing.B) {
	scenario := benchScenario(300)
	spec := RoundRobinPolicy()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RunWorld(scenario, spec); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*300)/b.Elapsed().Seconds(), "virtual-requests/sec")
}

func benchScenario(requests int) Scenario {
	const spacing = 5 * time.Millisecond
	arrivals := make([]Arrival, requests)
	for i := 0; i < requests; i++ {
		key := "/hot"
		if i%2 != 0 {
			key = "/cold"
		}
		arrivals[i] = Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: key}
	}
	return Scenario{
		Targets: []TargetProfile{
			{Name: "a", ServiceTime: 20 * time.Millisecond},
			{Name: "b", ServiceTime: 40 * time.Millisecond},
			{Name: "c", ServiceTime: 60 * time.Millisecond},
		},
		Arrivals: arrivals,
	}
}
