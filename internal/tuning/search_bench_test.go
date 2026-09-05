package tuning

import "testing"

// BenchmarkRunRandomSearch measures the tuner's own throughput --
// evaluations/sec and candidates/sec, master context rule 51's own
// named tuner metrics -- against a fixed, representative Development
// set, isolated from any one-off experiment's wall-clock printout.
func BenchmarkRunRandomSearch(b *testing.B) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 20)
	rsc := RandomSearchConfig{
		Evaluations:   1, // RunRandomSearch itself is called b.N times; each call does 1 evaluation
		OptimizerSeed: 1, ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rsc.OptimizerSeed = int64(i) // vary the seed so each call samples a fresh candidate, not a cached-identical one
		RunRandomSearch(rsc, scenarios)
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "evaluations/sec")
}
