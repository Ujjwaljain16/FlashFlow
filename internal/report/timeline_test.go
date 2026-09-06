package report

import (
	"testing"

	"flashflow/internal/replay"
)

// sampleWorldResult mirrors internal/backlog's own sampleTimeline
// fixture exactly (dispatches to "a" at t=0,5,10; completions at
// t=8,12,20 -- depth sequence 1,2,1,2,1,0), reused here so the
// hand-computed expectations below are directly checkable against
// internal/backlog's own already-verified DepthAt behavior.
func sampleWorldResult() *replay.WorldResult {
	return &replay.WorldResult{
		Records: []replay.SelectionRecord{
			{VirtualTimeMs: 0, Target: "a"},
			{VirtualTimeMs: 5, Target: "a"},
			{VirtualTimeMs: 10, Target: "a"},
		},
		Completions: []replay.CompletionRecord{
			{VirtualTimeMs: 8, Target: "a"},
			{VirtualTimeMs: 12, Target: "a"},
			{VirtualTimeMs: 20, Target: "a"},
		},
	}
}

// TestDepthSeries hand-verifies 4 buckets over [0,20]ms (step=5,
// midpoints 2.5/7.5/12.5/17.5): DepthAt(2.5)=1 (only the t=0 dispatch
// has occurred), DepthAt(7.5)=2 (t=0 and t=5 dispatches, no completion
// yet), DepthAt(12.5)=1 (t=0,5 dispatched; t=8 completed; t=10
// dispatched; t=12 completed -> net 1), DepthAt(17.5)=1 (unchanged
// since the last event at t=12).
func TestDepthSeries(t *testing.T) {
	wr := sampleWorldResult()
	got := DepthSeries(wr, "a", 4, 20)
	want := []SeriesPoint{{2.5, 1}, {7.5, 2}, {12.5, 1}, {17.5, 1}}
	if len(got) != len(want) {
		t.Fatalf("DepthSeries returned %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DepthSeries[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDepthSeries_DegenerateInputs(t *testing.T) {
	wr := sampleWorldResult()
	if got := DepthSeries(wr, "a", 0, 20); got != nil {
		t.Errorf("DepthSeries(buckets=0) = %v, want nil", got)
	}
	if got := DepthSeries(wr, "a", 4, 0); got != nil {
		t.Errorf("DepthSeries(horizonMs=0) = %v, want nil", got)
	}
}

// TestTrafficSeries hand-verifies the same 4-bucket/20ms window: one
// dispatch lands in each of the first three buckets (t=0,5,10 map to
// bucket indices 0,1,2 respectively, since step=5), the fourth bucket
// is empty.
func TestTrafficSeries(t *testing.T) {
	wr := sampleWorldResult()
	got := TrafficSeries(wr, 4, 20)
	want := []SeriesPoint{{2.5, 1}, {7.5, 1}, {12.5, 1}, {17.5, 0}}
	if len(got) != len(want) {
		t.Fatalf("TrafficSeries returned %d points, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TrafficSeries[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTrafficSeries_IgnoresEventsAtOrAfterHorizon(t *testing.T) {
	wr := &replay.WorldResult{Records: []replay.SelectionRecord{
		{VirtualTimeMs: 5, Target: "a"},
		{VirtualTimeMs: 25, Target: "a"}, // at/after horizon=20, must not count
	}}
	got := TrafficSeries(wr, 2, 20)
	total := got[0].Value + got[1].Value
	if total != 1 {
		t.Errorf("TrafficSeries total = %v, want 1 (the t=25 record is at/after the horizon and must be excluded)", total)
	}
}
