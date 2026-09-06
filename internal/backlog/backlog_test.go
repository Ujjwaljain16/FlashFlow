package backlog

import (
	"math"
	"testing"

	"flashflow/internal/replay"
)

// hand-computed timeline: dispatches to "a" at t=0,5,10; completions at
// t=8,12,20. Sorted events: (0,+1)(5,+1)(8,-1)(10,+1)(12,-1)(20,-1).
// Depth sequence: 1,2,1,2,1,0. Peak=2.
func sampleTimeline() Timeline {
	records := []replay.SelectionRecord{
		{VirtualTimeMs: 0, Target: "a"},
		{VirtualTimeMs: 5, Target: "a"},
		{VirtualTimeMs: 10, Target: "a"},
	}
	completions := []replay.CompletionRecord{
		{VirtualTimeMs: 8, Target: "a"},
		{VirtualTimeMs: 12, Target: "a"},
		{VirtualTimeMs: 20, Target: "a"},
	}
	return BuildTimeline(records, completions, "a")
}

func TestBuildTimeline_DepthAt(t *testing.T) {
	tl := sampleTimeline()
	cases := []struct {
		atMs float64
		want int
	}{
		{0, 1}, {4, 1}, {5, 2}, {6, 2}, {8, 1}, {9, 1}, {10, 2}, {11, 2}, {12, 1}, {19, 1}, {20, 0},
	}
	for _, c := range cases {
		if got := tl.DepthAt(c.atMs); got != c.want {
			t.Errorf("DepthAt(%v) = %d, want %d", c.atMs, got, c.want)
		}
	}
}

func TestBuildTimeline_PeakDepth(t *testing.T) {
	tl := sampleTimeline()
	if got := tl.PeakDepth(); got != 2 {
		t.Errorf("PeakDepth() = %d, want 2", got)
	}
}

func TestTimeline_AreaUnderCurve(t *testing.T) {
	tl := sampleTimeline()
	// Hand-computed: 1*(5-0) + 2*(8-5) + 1*(10-8) + 2*(12-10) + 1*(20-12) = 5+6+2+4+8 = 25.
	got := tl.AreaUnderCurve(20)
	if math.Abs(got-25) > 1e-9 {
		t.Errorf("AreaUnderCurve(20) = %v, want 25", got)
	}
}

func TestTimeline_TimeAboveThreshold(t *testing.T) {
	tl := sampleTimeline()
	// capacity=1, threshold=1.0: depth>1 during [5,8) and [10,12) = 3+2 = 5ms.
	got := tl.TimeAboveThreshold(1, 1.0, 20)
	if math.Abs(got-5) > 1e-9 {
		t.Errorf("TimeAboveThreshold(cap=1, thresh=1.0) = %v, want 5", got)
	}
	// A higher threshold (2.0) should never be exceeded here (peak ratio is exactly 2, not > 2).
	got2 := tl.TimeAboveThreshold(1, 2.0, 20)
	if got2 != 0 {
		t.Errorf("TimeAboveThreshold(cap=1, thresh=2.0) = %v, want 0", got2)
	}
	// FractionAboveThreshold must be TimeAboveThreshold / horizon.
	frac := tl.FractionAboveThreshold(1, 1.0, 20)
	if math.Abs(frac-0.25) > 1e-9 {
		t.Errorf("FractionAboveThreshold = %v, want 0.25", frac)
	}
}

func TestComputeConcentration(t *testing.T) {
	completed := map[string]int{"a": 6, "b": 3, "c": 1}
	m := ComputeConcentration(completed)
	if math.Abs(m.Top1Share-0.6) > 1e-9 {
		t.Errorf("Top1Share = %v, want 0.6", m.Top1Share)
	}
	if math.Abs(m.Top3Share-1.0) > 1e-9 {
		t.Errorf("Top3Share = %v, want 1.0", m.Top3Share)
	}
	// Hand-computed: -(0.6*log2(0.6) + 0.3*log2(0.3) + 0.1*log2(0.1)) ~= 1.29546.
	wantEntropy := 1.29546
	if math.Abs(m.EntropyBits-wantEntropy) > 1e-4 {
		t.Errorf("EntropyBits = %v, want ~%v", m.EntropyBits, wantEntropy)
	}
}

func TestComputeConcentration_Empty(t *testing.T) {
	m := ComputeConcentration(map[string]int{})
	if m.Top1Share != 0 || m.Top3Share != 0 || m.EntropyBits != 0 {
		t.Errorf("empty input should produce all-zero metrics, got %+v", m)
	}
}

// TestAnalyzeDiversion hand-verifies the full congestion -> commitment
// -> diversion -> drain pipeline on a small, fully worked scenario.
//
// Target "a", capacity=1. Dispatches to a at t=0,2,4; completions at
// t=12,14,16 (serial, one at a time). Dispatches to b at t=6,8,10,12.
//
// Congestion onset (ratio>1.0): depth reaches 2 at t=2 (0 -> depth 1,
// 2 -> depth 2) -- CongestionAtMs=2.
//
// Diversion (window=4, shareThreshold=0.5): scanning records from the
// first one at/after t=2 (index 1, the t=2 dispatch to a): window
// [t=2,4,6,8] has a-share 2/4=0.5 (not STRICTLY below threshold);
// window [t=4,6,8,10] has a-share 1/4=0.25 (<0.5) -- DiversionAtMs=10
// (the window's last timestamp).
//
// Committed backlog: dispatches to "a" in [2,10) = t=2 and t=4 = 2.
//
// Queue drain: first time at/after t=10 that a's depth/capacity <= 1.0.
// Depth timeline for a: +1@0(=1), +1@2(=2), +1@4(=3), -1@12(=2), -1@14(=1).
// At t=12, ratio=2 (not <=1); at t=14, ratio=1 (<=1) -- QueueDrainAtMs=14.
func TestAnalyzeDiversion(t *testing.T) {
	records := []replay.SelectionRecord{
		{VirtualTimeMs: 0, Target: "a"},
		{VirtualTimeMs: 2, Target: "a"},
		{VirtualTimeMs: 4, Target: "a"},
		{VirtualTimeMs: 6, Target: "b"},
		{VirtualTimeMs: 8, Target: "b"},
		{VirtualTimeMs: 10, Target: "b"},
		{VirtualTimeMs: 12, Target: "b"},
	}
	completions := []replay.CompletionRecord{
		{VirtualTimeMs: 12, Target: "a"},
		{VirtualTimeMs: 14, Target: "a"},
		{VirtualTimeMs: 16, Target: "a"},
	}
	tl := BuildTimeline(records, completions, "a")
	cfg := CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 4, DiversionShareThreshold: 0.5}
	onset, found := FindFirstCongestionOnset(tl, 1, 1.0, 0)
	result := AnalyzeDiversion(records, tl, "a", 1, onset, found, cfg, 20)

	if !result.CongestionFound || result.CongestionAtMs != 2 {
		t.Errorf("CongestionAtMs = %v (found=%v), want 2 (found=true)", result.CongestionAtMs, result.CongestionFound)
	}
	if !result.DiversionFound || result.DiversionAtMs != 10 {
		t.Errorf("DiversionAtMs = %v (found=%v), want 10 (found=true)", result.DiversionAtMs, result.DiversionFound)
	}
	if result.CommittedBacklog != 2 {
		t.Errorf("CommittedBacklog = %d, want 2", result.CommittedBacklog)
	}
	if !result.QueueDrainFound || result.QueueDrainAtMs != 14 {
		t.Errorf("QueueDrainAtMs = %v (found=%v), want 14 (found=true)", result.QueueDrainAtMs, result.QueueDrainFound)
	}
}

// TestFindPeakEpisodeCongestionOnset_MultipleEpisodes hand-verifies that
// the peak-anchored onset finder correctly picks the SECOND, larger
// episode's onset rather than the first, smaller one -- the exact
// scenario experiment-015a's canonical run exposed (EWMA's target had a
// tiny early congestion blip that a naive "first congestion ever" onset
// picked, followed by a much larger episode during the actual workload
// peak that a first-episode-only analysis silently ignored).
//
// Episode 1: dispatches at t=0,2 (peak depth 2), drains by t=4.
// Episode 2 (bigger, later): dispatches at t=20,22,24,26 (peak depth 4),
// drains by t=46. The peak-episode onset must be t=22 (episode 2's
// start), not t=2 (episode 1's start).
func TestFindPeakEpisodeCongestionOnset_MultipleEpisodes(t *testing.T) {
	records := []replay.SelectionRecord{
		{VirtualTimeMs: 0, Target: "a"},
		{VirtualTimeMs: 2, Target: "a"},
		{VirtualTimeMs: 20, Target: "a"},
		{VirtualTimeMs: 22, Target: "a"},
		{VirtualTimeMs: 24, Target: "a"},
		{VirtualTimeMs: 26, Target: "a"},
	}
	completions := []replay.CompletionRecord{
		{VirtualTimeMs: 3, Target: "a"},
		{VirtualTimeMs: 4, Target: "a"},
		{VirtualTimeMs: 40, Target: "a"},
		{VirtualTimeMs: 42, Target: "a"},
		{VirtualTimeMs: 44, Target: "a"},
		{VirtualTimeMs: 46, Target: "a"},
	}
	tl := BuildTimeline(records, completions, "a")
	if peak := tl.PeakDepth(); peak != 4 {
		t.Fatalf("PeakDepth() = %d, want 4 (sanity check before testing onset)", peak)
	}
	onset, found := FindPeakEpisodeCongestionOnset(tl, 1, 1.0)
	if !found || onset != 22 {
		t.Errorf("FindPeakEpisodeCongestionOnset = %v (found=%v), want 22 (found=true) -- must anchor to the SECOND, larger episode, not the first", onset, found)
	}
	// Contrast: the naive "first ever" onset finder should still report
	// the smaller, earlier episode -- confirming the two functions
	// genuinely differ, not that one is a no-op wrapper of the other.
	firstOnset, firstFound := FindFirstCongestionOnset(tl, 1, 1.0, 0)
	if !firstFound || firstOnset != 2 {
		t.Errorf("FindFirstCongestionOnset = %v (found=%v), want 2 (found=true)", firstOnset, firstFound)
	}
}

// TestAnalyzeDiversion_CongestionButNeverDiverts guards a real bug found
// while building Stage 16's flagship demonstration: a policy whose own
// share of dispatches to a congested target NEVER drops below the
// diversion threshold (e.g. weighted-round-robin, whose static weights
// keep its share constant for the whole run) must report
// DiversionFound=false -- NOT the misleading combination of
// CommittedBacklog=0 and QueueDrainFound=false, which reads as "zero
// backlog, but the queue never recovers" when the true meaning is "this
// metric never applies to this policy's failure mode at all." The
// flagship experiment's own first version made exactly this mistake
// before DiversionFound was threaded through to its output.
//
// Target "a" repeatedly gets 2-of-every-3 dispatches (a,a,b pattern),
// share=0.667, which never drops below a 0.5 diversion threshold in any
// trailing 3-record window, while "a" itself never completes within the
// observed window (so it stays congested the whole time).
func TestAnalyzeDiversion_CongestionButNeverDiverts(t *testing.T) {
	var records []replay.SelectionRecord
	for i := 0; i < 12; i++ {
		target := "a"
		if i%3 == 2 {
			target = "b"
		}
		records = append(records, replay.SelectionRecord{VirtualTimeMs: float64(i), Target: target})
	}
	var completions []replay.CompletionRecord
	for i := 0; i < 8; i++ {
		completions = append(completions, replay.CompletionRecord{VirtualTimeMs: 50, Target: "a"})
	}
	tl := BuildTimeline(records, completions, "a")
	cfg := CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 3, DiversionShareThreshold: 0.5}
	onset, found := FindFirstCongestionOnset(tl, 1, 1.0, 0)
	if !found || onset != 1 {
		t.Fatalf("FindFirstCongestionOnset = %v (found=%v), want 1 (found=true) -- test setup assumption violated", onset, found)
	}
	result := AnalyzeDiversion(records, tl, "a", 1, onset, found, cfg, 20)
	if !result.CongestionFound {
		t.Errorf("expected CongestionFound=true, got false")
	}
	if result.DiversionFound {
		t.Errorf("expected DiversionFound=false (share never drops below threshold), got true with DiversionAtMs=%v", result.DiversionAtMs)
	}
	if result.CommittedBacklog != 0 || result.QueueDrainFound {
		t.Errorf("when DiversionFound=false, CommittedBacklog and QueueDrainFound must stay at their zero-value defaults (meaning N/A, not 'zero backlog' or 'never drains') -- got %+v", result)
	}
}

// TestAnalyzeDiversion_NoCongestion confirms a target that never crosses
// the ratio threshold reports CongestionFound=false and nothing further
// is computed (no false-positive diversion/backlog numbers).
func TestAnalyzeDiversion_NoCongestion(t *testing.T) {
	records := []replay.SelectionRecord{
		{VirtualTimeMs: 0, Target: "a"},
		{VirtualTimeMs: 10, Target: "a"},
	}
	completions := []replay.CompletionRecord{
		{VirtualTimeMs: 5, Target: "a"},
		{VirtualTimeMs: 15, Target: "a"},
	}
	tl := BuildTimeline(records, completions, "a")
	cfg := CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 2, DiversionShareThreshold: 0.5}
	onset, found := FindFirstCongestionOnset(tl, 1, 1.0, 0)
	result := AnalyzeDiversion(records, tl, "a", 1, onset, found, cfg, 20)
	if result.CongestionFound {
		t.Errorf("expected CongestionFound=false for a target that never exceeds ratio 1.0, got %+v", result)
	}
	if result.DiversionFound || result.CommittedBacklog != 0 || result.QueueDrainFound {
		t.Errorf("no downstream fields should be set when congestion was never found, got %+v", result)
	}
}
