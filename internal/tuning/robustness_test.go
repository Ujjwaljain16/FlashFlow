package tuning

import (
	"testing"

	"flashflow/internal/proxy"
)

func TestPerScenarioUtility_OneEntryPerScenario(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 8)
	utilities, err := PerScenarioUtility(proxy.DefaultAdaptiveConfig(), scenarios, DefaultObjectiveWeights())
	if err != nil {
		t.Fatalf("PerScenarioUtility failed: %v", err)
	}
	if len(utilities) != len(scenarios) {
		t.Fatalf("expected %d utilities, got %d", len(scenarios), len(utilities))
	}
	for i, u := range utilities {
		if u < 0 || u > 1 {
			t.Fatalf("utility %d out of plausible [0,1] range: %v", i, u)
		}
	}
}

func TestComputeRobustness_KnownValues(t *testing.T) {
	r, err := ComputeRobustness([]float64{0.2, 0.4, 0.6, 0.8, 1.0})
	if err != nil {
		t.Fatalf("ComputeRobustness failed: %v", err)
	}
	if r.Mean != 0.6 {
		t.Errorf("expected mean 0.6, got %v", r.Mean)
	}
	if r.Median != 0.6 {
		t.Errorf("expected median 0.6, got %v", r.Median)
	}
	if r.Worst != 0.2 {
		t.Errorf("expected worst 0.2, got %v", r.Worst)
	}
	if r.StdDev <= 0 {
		t.Errorf("expected a positive StdDev for a spread-out sample, got %v", r.StdDev)
	}
}

func TestComputeRobustness_ZeroVarianceGivesZeroStdDev(t *testing.T) {
	r, err := ComputeRobustness([]float64{0.5, 0.5, 0.5})
	if err != nil {
		t.Fatalf("ComputeRobustness failed: %v", err)
	}
	if r.StdDev != 0 {
		t.Errorf("expected StdDev 0 for a constant sample, got %v", r.StdDev)
	}
	if r.Worst != 0.5 {
		t.Errorf("expected worst 0.5, got %v", r.Worst)
	}
}
