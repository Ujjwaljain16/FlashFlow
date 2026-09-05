package tuning

import (
	"testing"

	"flashflow/internal/proxy"
)

func TestRunSensitivityAnalysis_ProducesTwelvePerturbations(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 8)
	report, err := RunSensitivityAnalysis(proxy.DefaultAdaptiveConfig(), scenarios, DefaultObjectiveWeights(), DefaultConfigSpace())
	if err != nil {
		t.Fatalf("RunSensitivityAnalysis failed: %v", err)
	}
	// 4 weights x 2 directions + 2 durations x 2 directions = 12.
	if len(report.Perturbations) != 12 {
		t.Fatalf("expected 12 perturbations, got %d", len(report.Perturbations))
	}
}

func TestRunSensitivityAnalysis_PerturbedWeightsStayOnSimplex(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 5)
	cs := DefaultConfigSpace()
	report, err := RunSensitivityAnalysis(proxy.DefaultAdaptiveConfig(), scenarios, DefaultObjectiveWeights(), cs)
	if err != nil {
		t.Fatalf("RunSensitivityAnalysis failed: %v", err)
	}
	for _, p := range report.Perturbations {
		if ok, reason := cs.Valid(p.Config); !ok {
			t.Fatalf("perturbation %s%s produced an invalid config: %s (%+v)", p.Parameter, p.Direction, reason, p.Config)
		}
	}
}

func TestRunSensitivityAnalysis_DurationPerturbationsRespectBounds(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 5)
	cs := DefaultConfigSpace()
	// A baseline already at the space's lower StaleAfter bound: a -100ms
	// perturbation must clamp rather than underflow below the bound.
	edgeCfg := proxy.DefaultAdaptiveConfig()
	edgeCfg.StaleAfter = cs.StaleAfterMin
	report, err := RunSensitivityAnalysis(edgeCfg, scenarios, DefaultObjectiveWeights(), cs)
	if err != nil {
		t.Fatalf("RunSensitivityAnalysis failed: %v", err)
	}
	for _, p := range report.Perturbations {
		if p.Config.StaleAfter < cs.StaleAfterMin || p.Config.StaleAfter > cs.StaleAfterMax {
			t.Fatalf("perturbation %s%s produced StaleAfter %v outside [%v, %v]",
				p.Parameter, p.Direction, p.Config.StaleAfter, cs.StaleAfterMin, cs.StaleAfterMax)
		}
	}
}

func TestRunSensitivityAnalysis_DeltasAreRelativeToBaseline(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 8)
	report, err := RunSensitivityAnalysis(proxy.DefaultAdaptiveConfig(), scenarios, DefaultObjectiveWeights(), DefaultConfigSpace())
	if err != nil {
		t.Fatalf("RunSensitivityAnalysis failed: %v", err)
	}
	for _, p := range report.Perturbations {
		want := p.Utility - report.BaselineUtility
		if p.Delta != want {
			t.Fatalf("perturbation %s%s: Delta=%v, want Utility-BaselineUtility=%v", p.Parameter, p.Direction, p.Delta, want)
		}
	}
}
