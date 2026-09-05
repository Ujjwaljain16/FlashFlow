package tuning

import (
	"testing"
	"time"

	"flashflow/internal/replay"
)

func completionsOf(latenciesMs ...float64) []replay.CompletionRecord {
	out := make([]replay.CompletionRecord, len(latenciesMs))
	for i, ms := range latenciesMs {
		out[i] = replay.CompletionRecord{Target: "t", Latency: time.Duration(ms * float64(time.Millisecond))}
	}
	return out
}

func TestComputeMetrics_NoCompletionsReturnsError(t *testing.T) {
	_, err := ComputeMetrics([]replay.WorldResult{{}})
	if err != ErrNoCompletions {
		t.Fatalf("expected ErrNoCompletions, got %v", err)
	}
}

func TestComputeMetrics_PoolsAcrossScenarios(t *testing.T) {
	results := []replay.WorldResult{
		{
			Completions:       completionsOf(10, 10, 10, 10),
			CompletedByTarget: map[string]int{"a": 4},
		},
		{
			Completions:       completionsOf(100, 100),
			CompletedByTarget: map[string]int{"a": 1, "b": 1},
			RejectedCount:     1,
		},
	}
	m, err := ComputeMetrics(results)
	if err != nil {
		t.Fatalf("ComputeMetrics failed: %v", err)
	}
	// Pooled sample: [10,10,10,10,100,100] (6 points). p50 should be
	// near the low cluster, not blown up by the high one.
	if m.P50LatencyMs > 50 {
		t.Fatalf("expected pooled p50 to stay near the dominant low-latency cluster, got %v", m.P50LatencyMs)
	}
	// 1 rejected out of 1 rejected + 6 completed = 1/7.
	wantReject := 1.0 / 7.0
	if diff := m.RejectedRate - wantReject; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected RejectedRate %v, got %v", wantReject, m.RejectedRate)
	}
	// MeanMaxShare: scenario 1 max share = 4/4 = 1.0; scenario 2 max
	// share = 1/2 = 0.5. Mean = 0.75.
	if diff := m.MeanMaxShare - 0.75; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected MeanMaxShare 0.75, got %v", m.MeanMaxShare)
	}
}

// TestComputeScores_KnownCases validates the bounded transforms against
// hand-computed cases before this objective is trusted on any real
// tuning run -- the same synthetic-validation-first discipline 006-A
// and 007-A established.
func TestComputeScores_KnownCases(t *testing.T) {
	cases := []struct {
		name string
		m    Metrics
		want Scores
	}{
		{
			name: "zero latency, zero rejects, perfectly even (2 targets)",
			m:    Metrics{MeanLatencyMs: 0, RejectedRate: 0, MeanMaxShare: 0.5},
			want: Scores{LatencyScore: 1, RejectScore: 1, FairnessScore: 0.5},
		},
		{
			name: "latency exactly at the reference point",
			m:    Metrics{MeanLatencyMs: RefLatencyMs, RejectedRate: 0, MeanMaxShare: 0.5},
			want: Scores{LatencyScore: 0.5, RejectScore: 1, FairnessScore: 0.5},
		},
		{
			name: "all rejected",
			m:    Metrics{MeanLatencyMs: 0, RejectedRate: 1, MeanMaxShare: 0},
			want: Scores{LatencyScore: 1, RejectScore: 0, FairnessScore: 1},
		},
		{
			name: "total concentration on one target",
			m:    Metrics{MeanLatencyMs: 0, RejectedRate: 0, MeanMaxShare: 1},
			want: Scores{LatencyScore: 1, RejectScore: 1, FairnessScore: 0},
		},
	}
	for _, c := range cases {
		got := ComputeScores(c.m)
		if got != c.want {
			t.Errorf("%s: ComputeScores(%+v) = %+v, want %+v", c.name, c.m, got, c.want)
		}
	}
}

// TestComputeScores_UsesMeanLatencyNotP99 is an explicit, named regression
// test for the mid-Stage-8 objective-function correction: LatencyScore
// was originally computed from p99 latency, uninformative at this
// project's sample size (any policy sending even a few requests to the
// worst target pins p99 to that target's raw service time regardless of
// overall routing quality), and was corrected to mean latency. Unlike
// TestComputeScores_KnownCases' "latency exactly at the reference point"
// case -- which only incidentally guards this, since it leaves
// P99LatencyMs at its zero-value default -- this test sets both fields to
// different, deliberately distinguishing values, so it fails specifically
// if ComputeScores is ever changed (refactored, "cleaned up," or
// reverted) to read P99LatencyMs instead of MeanLatencyMs.
func TestComputeScores_UsesMeanLatencyNotP99(t *testing.T) {
	m := Metrics{MeanLatencyMs: RefLatencyMs, P99LatencyMs: RefLatencyMs * 10, RejectedRate: 0, MeanMaxShare: 0}
	got := ComputeScores(m)
	if got.LatencyScore != 0.5 {
		t.Fatalf("expected LatencyScore=0.5 (derived from MeanLatencyMs=RefLatencyMs), got %v -- ComputeScores may be reading P99LatencyMs (%v) instead of MeanLatencyMs (%v)",
			got.LatencyScore, m.P99LatencyMs, m.MeanLatencyMs)
	}
}

func TestUtility_HigherScoresAlwaysRankHigher(t *testing.T) {
	w := DefaultObjectiveWeights()
	better := Scores{LatencyScore: 0.9, RejectScore: 0.9, FairnessScore: 0.9}
	worse := Scores{LatencyScore: 0.1, RejectScore: 0.1, FairnessScore: 0.1}
	if Utility(better, w) <= Utility(worse, w) {
		t.Fatalf("a strictly-better-on-every-dimension score did not rank higher: %v vs %v", Utility(better, w), Utility(worse, w))
	}
}

func TestParetoFrontier_KnownCase(t *testing.T) {
	// A: better latency, worse fairness. B: better fairness, worse
	// latency. C: dominated by A on every dimension. Expect {A, B} on
	// the frontier, C excluded -- the exact "neither A nor B dominates"
	// scenario master context rule 7 describes.
	scores := []Scores{
		{LatencyScore: 0.9, RejectScore: 0.8, FairnessScore: 0.3}, // A
		{LatencyScore: 0.5, RejectScore: 0.8, FairnessScore: 0.9}, // B
		{LatencyScore: 0.8, RejectScore: 0.7, FairnessScore: 0.2}, // C, dominated by A
	}
	frontier := ParetoFrontier(scores)
	want := map[int]bool{0: true, 1: true}
	if len(frontier) != len(want) {
		t.Fatalf("expected frontier %v, got indices %v", want, frontier)
	}
	for _, idx := range frontier {
		if !want[idx] {
			t.Fatalf("index %d should not be on the Pareto frontier (scores %+v)", idx, scores[idx])
		}
	}
}

func TestParetoFrontier_SingleDominantCandidateIsAloneOnFrontier(t *testing.T) {
	scores := []Scores{
		{LatencyScore: 0.9, RejectScore: 0.9, FairnessScore: 0.9},
		{LatencyScore: 0.1, RejectScore: 0.1, FairnessScore: 0.1},
		{LatencyScore: 0.5, RejectScore: 0.5, FairnessScore: 0.5},
	}
	frontier := ParetoFrontier(scores)
	if len(frontier) != 1 || frontier[0] != 0 {
		t.Fatalf("expected only index 0 on the frontier, got %v", frontier)
	}
}
