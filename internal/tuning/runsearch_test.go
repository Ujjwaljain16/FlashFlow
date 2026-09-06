package tuning

import (
	"testing"

	"flashflow/internal/replay"
)

func TestTuners_AllSatisfyInterface(t *testing.T) {
	var _ Tuner = NewRandomSearchTuner(1, DefaultConfigSpace())
	var _ Tuner = NewLHSTuner(1, DefaultConfigSpace(), 10)
	var _ Tuner = NewBayesOptTuner(1, DefaultConfigSpace())
}

func testScenariosForSearch(t *testing.T) []replay.Scenario {
	t.Helper()
	split := NewSplit(DefaultScenarioSpace())
	return split.Development[:10]
}

func TestRunSearch_WorksWithEveryTuner(t *testing.T) {
	scenarios := testScenariosForSearch(t)
	weights := DefaultObjectiveWeights()

	tuners := []Tuner{
		NewRandomSearchTuner(1, DefaultConfigSpace()),
		NewLHSTuner(1, DefaultConfigSpace(), 15),
		NewBayesOptTuner(1, DefaultConfigSpace()),
	}
	for _, tuner := range tuners {
		result := RunSearch(tuner, 15, scenarios, weights)
		if len(result.Evaluations) != 15 {
			t.Errorf("%s: got %d evaluations, want 15", tuner.Name(), len(result.Evaluations))
		}
		if result.TunerVersion != tuner.Name() {
			t.Errorf("%s: TunerVersion = %q, want %q", tuner.Name(), result.TunerVersion, tuner.Name())
		}
		if _, ok := result.Best(); !ok {
			t.Errorf("%s: expected at least one valid evaluation", tuner.Name())
		}
		for _, e := range result.Evaluations {
			if !e.Valid {
				t.Errorf("%s: every candidate from a Tuner sampling ConfigSpace directly should be valid, got invalid: %s", tuner.Name(), e.InvalidReason)
			}
		}
	}
}

func TestRunRandomSearch_StillWorksUnchanged(t *testing.T) {
	scenarios := testScenariosForSearch(t)
	rsc := RandomSearchConfig{Evaluations: 15, OptimizerSeed: 1, ConfigSpace: DefaultConfigSpace(), ObjectiveWeights: DefaultObjectiveWeights()}
	result := RunRandomSearch(rsc, scenarios)
	if result.TunerVersion != TunerVersion {
		t.Errorf("TunerVersion = %q, want %q", result.TunerVersion, TunerVersion)
	}
	if result.OptimizerSeed != 1 {
		t.Errorf("OptimizerSeed = %d, want 1", result.OptimizerSeed)
	}
	if len(result.Evaluations) != 15 {
		t.Errorf("got %d evaluations, want 15", len(result.Evaluations))
	}
}
