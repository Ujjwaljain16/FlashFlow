package tuning

import (
	"testing"

	"flashflow/internal/proxy"
)

func TestEvaluate_DefaultConfigProducesSensibleScores(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 10)
	_, scores, err := Evaluate(proxy.DefaultAdaptiveConfig(), scenarios)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if scores.LatencyScore <= 0 || scores.LatencyScore > 1 {
		t.Fatalf("LatencyScore out of (0,1]: %v", scores.LatencyScore)
	}
	if scores.RejectScore < 0 || scores.RejectScore > 1 {
		t.Fatalf("RejectScore out of [0,1]: %v", scores.RejectScore)
	}
	if scores.FairnessScore <= 0 || scores.FairnessScore > 1 {
		t.Fatalf("FairnessScore out of (0,1]: %v", scores.FairnessScore)
	}
}

func TestEvaluate_IsDeterministicForTheSameConfigAndScenarios(t *testing.T) {
	scenarios := DefaultScenarioSpace().GenerateSet(1, 5)
	cfg := proxy.DefaultAdaptiveConfig()
	m1, s1, err := Evaluate(cfg, scenarios)
	if err != nil {
		t.Fatalf("Evaluate (1st) failed: %v", err)
	}
	m2, s2, err := Evaluate(cfg, scenarios)
	if err != nil {
		t.Fatalf("Evaluate (2nd) failed: %v", err)
	}
	if m1 != m2 || s1 != s2 {
		t.Fatalf("identical config+scenarios produced diverging evaluations: %+v/%+v vs %+v/%+v", m1, s1, m2, s2)
	}
}
