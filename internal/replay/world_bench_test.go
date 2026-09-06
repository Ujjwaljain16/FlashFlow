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

// BenchmarkRunWorld_AdaptivePolicy_Contention is Stage 12's own required
// cost measurement (docs/StageArtifacts/Stage12.md §29): the identical
// scenario/policy as BenchmarkRunWorld_AdaptivePolicy above, with
// Capacity=1 enabled on every target -- directly comparable via `go test
// -bench` output, showing exactly what the new finite-capacity queueing
// machinery costs relative to the flat model it's layered on top of.
func BenchmarkRunWorld_AdaptivePolicy_Contention(b *testing.B) {
	scenario := benchScenarioWithCapacity(300, 1)
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

func BenchmarkRunWorld_RoundRobinPolicy_Contention(b *testing.B) {
	scenario := benchScenarioWithCapacity(300, 1)
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

func benchScenarioWithCapacity(requests, capacity int) Scenario {
	s := benchScenario(requests)
	for i := range s.Targets {
		s.Targets[i].Capacity = capacity
	}
	return s
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
