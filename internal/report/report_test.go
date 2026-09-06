package report

import (
	"testing"

	"flashflow/internal/backlog"
	"flashflow/internal/replay"
)

func TestClassify_Stable_NeverCongested(t *testing.T) {
	m := Metrics{CongestionFound: false}
	c, _ := Classify(m)
	if c != Stable {
		t.Errorf("Classify(never congested) = %v, want Stable", c)
	}
}

func TestClassify_ChronicCollapse_NeverConcentratesNeverDrains(t *testing.T) {
	// Mirrors round-robin's own canonical-scenario data (Stage 15/16):
	// concentration_ratio=1.06 (essentially fair share -- never
	// genuinely concentrates), drains=false.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 1.06, Drained: false}
	c, _ := Classify(m)
	if c != ChronicCollapse {
		t.Errorf("Classify(concentration=1.06, drained=false) = %v, want ChronicCollapse", c)
	}
}

func TestClassify_RecoveryLimited_NeverConcentratesButSevereBacklogDrains(t *testing.T) {
	// Unconcentrated (never locks onto this target above fair share) but
	// still committed a severe amount of work (e.g. a structurally
	// over-allocated but not fully static policy) before eventually
	// draining -- distinct from Stable, which requires LOW committed
	// work regardless of concentration.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 1.1, Drained: true, DrainAtMs: 7000, CommittedWork: 50, Capacity: 1}
	c, _ := Classify(m)
	if c != RecoveryLimited {
		t.Errorf("Classify(concentration=1.1, drained=true, committed_work=50) = %v, want RecoveryLimited", c)
	}
}

func TestClassify_Stable_NeverConcentratesAndDrainsWithMinimalWork(t *testing.T) {
	// Mirrors P2C-load's own canonical-scenario data: sampling-bounded
	// selection never concentrates strongly, but critically also never
	// accumulates meaningful backlog (committed_work=4) -- this must
	// classify as Stable, not RecoveryLimited, since low committed work
	// is itself a good outcome regardless of concentration. An earlier,
	// asymmetric version of this tree only checked committed work on
	// the CONCENTRATED branch and misclassified this exact shape.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 0.9, Drained: true, CommittedWork: 4, Capacity: 1}
	c, _ := Classify(m)
	if c != Stable {
		t.Errorf("Classify(concentration=0.9, drained=true, committed_work=4) = %v, want Stable", c)
	}
}

func TestClassify_AcuteCollapse_ConcentratesAndNeverDrains(t *testing.T) {
	m := Metrics{CongestionFound: true, ConcentrationRatio: 2.5, Drained: false}
	c, _ := Classify(m)
	if c != AcuteCollapse {
		t.Errorf("Classify(concentration=2.5, drained=false) = %v, want AcuteCollapse", c)
	}
}

func TestClassify_AcuteCollapse_LargeCommittedWork(t *testing.T) {
	// Mirrors EWMA's own canonical-scenario data: concentration_ratio~2.5
	// (well above fair share), drained=true, committed_work=97,
	// capacity=1 -> ratio=97, way over the 10x threshold. This is the
	// exact case an earlier fraction-of-time-over-capacity-based
	// version of this classifier misclassified as RecoveryLimited.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 2.5, Drained: true, CommittedWork: 97, Capacity: 1}
	c, _ := Classify(m)
	if c != AcuteCollapse {
		t.Errorf("Classify(committed_work=97, capacity=1) = %v, want AcuteCollapse", c)
	}
}

func TestClassify_Stable_SmallCommittedWork(t *testing.T) {
	// Mirrors least-connections' own canonical-scenario data:
	// concentration_ratio~3.1, drained=true, committed_work=8,
	// capacity=1 -> ratio=8, under the 10x threshold.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 3.1, Drained: true, CommittedWork: 8, Capacity: 1}
	c, _ := Classify(m)
	if c != Stable {
		t.Errorf("Classify(committed_work=8, capacity=1) = %v, want Stable", c)
	}
}

func TestClassify_BoundaryIsInclusive(t *testing.T) {
	// Exactly at the 10x boundary must count as acute (>=), not stable.
	m := Metrics{CongestionFound: true, ConcentrationRatio: 2.0, Drained: true, CommittedWork: 10, Capacity: 1}
	c, _ := Classify(m)
	if c != AcuteCollapse {
		t.Errorf("Classify(committed_work exactly at 10x capacity) = %v, want AcuteCollapse (boundary must be inclusive)", c)
	}
}

func TestMechanism_KnownAndUnknownPolicies(t *testing.T) {
	if got := Mechanism("ewma"); got != "SMOOTHED-HISTORY LOCK-IN" {
		t.Errorf("Mechanism(ewma) = %q, want SMOOTHED-HISTORY LOCK-IN", got)
	}
	if got := Mechanism("not-a-real-policy"); got == "" {
		t.Errorf("Mechanism(unknown) returned empty string, want a non-empty fallback")
	}
}

// TestAnalyzeTarget_ChronicRoundRobinShape hand-constructs the exact
// mechanism experiment-016-flagship found for round-robin: a target
// that is dispatched to at a constant, moderate rate throughout (never
// materially "diverted" from in a reactive sense) and never drains --
// AnalyzeTarget must classify this via FractionAboveCapacity, not via
// DiversionFound, or round-robin's chronic failure would be
// misclassified as acute (the exact bug this classifier design
// deliberately avoids -- see the plan's own "Key Finding" section).
func TestAnalyzeTarget_ChronicRoundRobinShape(t *testing.T) {
	target := "slow"
	other := "fast"
	capacity := 1
	horizonMs := 8000.0

	var records []replay.SelectionRecord
	var completions []replay.CompletionRecord
	// "slow" receives one dispatch every 20ms while its own service time
	// is 60ms, so three requests are perpetually in flight (60/20=3,
	// always over Capacity=1) for the entire horizon -- round-robin's
	// own even, unchanging split producing a genuinely STEADY-STATE
	// overload, not a one-time spike. Completions past the horizon are
	// deliberately NOT included (a real replay.WorldResult never
	// contains a completion beyond its own Horizon -- those requests
	// stay InFlightAtHorizon instead), so this doesn't manufacture a
	// false "drain" purely because dispatches happened to stop.
	for i := 0; i < 400; i++ {
		tMs := float64(i) * 20
		if tMs >= horizonMs {
			break
		}
		records = append(records, replay.SelectionRecord{VirtualTimeMs: tMs, Target: target})
		records = append(records, replay.SelectionRecord{VirtualTimeMs: tMs + 10, Target: other})
		if completeAt := tMs + 60; completeAt < horizonMs {
			completions = append(completions, replay.CompletionRecord{VirtualTimeMs: completeAt, Target: target})
		}
		if completeAt := tMs + 15; completeAt < horizonMs {
			completions = append(completions, replay.CompletionRecord{VirtualTimeMs: completeAt, Target: other})
		}
	}
	targets := []replay.TargetProfile{
		{Name: target, Capacity: capacity},
		{Name: other, Capacity: capacity},
	}
	wr := &replay.WorldResult{Records: records, Completions: completions}
	cfg := backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 0.5}

	m := AnalyzeTarget(wr, targets, capacity, horizonMs, cfg)
	if m.Bottleneck != target {
		t.Fatalf("AnalyzeTarget bottleneck = %q, want %q", m.Bottleneck, target)
	}
	if !m.CongestionFound {
		t.Fatalf("expected CongestionFound=true for a target dispatched to every 20ms with a 60ms service time")
	}
	class, _ := Classify(m)
	if class != ChronicCollapse {
		t.Errorf("Classify(round-robin-shaped chronic target) = %v, want ChronicCollapse (fraction_above_capacity=%.3f, drained=%v)", class, m.FractionAboveCapacity, m.Drained)
	}
}
