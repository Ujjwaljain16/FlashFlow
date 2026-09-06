// Package backlog is Stage 15's one new analysis package: reusable,
// deterministic backlog-dynamics metrics computed purely from
// replay.WorldResult's existing Records/Completions event streams,
// added because internal/attribution's existing Utilization/
// CheckLittlesLaw/Explain compute only a single whole-run rho per
// target (confirmed by direct inspection before writing this package,
// per Stage 15's own charter against burying calculations in experiment
// binaries) -- no time-resolved queue depth, no duration-above-
// threshold, and no committed-backlog concept at all.
//
// Every function here takes plain slices/maps already present on
// replay.WorldResult; nothing in RunWorld itself changes, and no new
// TargetProfile field or trace event type is introduced. This mirrors
// the exact reconstruction technique experiment-014h's own
// queueDrainTimeMs first established (merge dispatch (+1) and
// completion (-1) events into one chronological timeline), generalized
// here into a shared, tested implementation instead of being
// re-duplicated in a tenth experiment binary.
package backlog

import (
	"math"
	"sort"

	"flashflow/internal/replay"
)

// Event is one +1 (dispatch) or -1 (completion) change to a single
// target's queue depth (busy-plus-waiting count).
type Event struct {
	TimeMs float64
	Delta  int
}

// Timeline is one target's reconstructed depth-over-time series: the
// exact count of requests dispatched to, but not yet completed by, that
// target at any point during the run. Depth only changes at Events;
// between events it is piecewise constant.
type Timeline struct {
	Events []Event // sorted ascending by TimeMs; ties broken dispatch-before-completion, see BuildTimeline
}

// BuildTimeline reconstructs target's own queue-depth timeline from the
// world's raw dispatch and completion records. This is an aggregate
// reconstruction (total dispatches minus total completions over time),
// not a per-request trace -- correct because the underlying simulation
// is single-threaded and deterministic, so at any instant the count of
// "dispatched but not yet completed" requests for one target is exactly
// its true in-flight (busy+queued) count, matching the same
// finite-capacity FIFO model internal/replay/world.go itself implements.
//
// Tie-breaking: at an identical timestamp, a completion is applied
// BEFORE a dispatch (completions free a slot before any request that
// arrives at literally the same instant could use it) -- this matches
// world.go's own event-scheduling order and avoids an off-by-one depth
// spike at simultaneous dispatch/completion instants.
func BuildTimeline(records []replay.SelectionRecord, completions []replay.CompletionRecord, target string) Timeline {
	var events []Event
	for _, r := range records {
		if r.Target == target {
			events = append(events, Event{TimeMs: r.VirtualTimeMs, Delta: 1})
		}
	}
	for _, c := range completions {
		if c.Target == target {
			events = append(events, Event{TimeMs: c.VirtualTimeMs, Delta: -1})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].TimeMs != events[j].TimeMs {
			return events[i].TimeMs < events[j].TimeMs
		}
		return events[i].Delta < events[j].Delta // -1 before +1 at a tie
	})
	return Timeline{Events: events}
}

// DepthAt returns the queue depth immediately after applying every event
// at or before timeMs.
func (t Timeline) DepthAt(timeMs float64) int {
	depth := 0
	for _, e := range t.Events {
		if e.TimeMs > timeMs {
			break
		}
		depth += e.Delta
	}
	return depth
}

// PeakDepth returns the maximum depth ever reached (M2's "peak queue
// depth" candidate).
func (t Timeline) PeakDepth() int {
	peak, _ := t.PeakDepthAt()
	return peak
}

// PeakDepthAt is PeakDepth plus the time the peak first occurred --
// needed by anything that wants to reason about what happened AFTER
// the worst moment (e.g. internal/report's DrainedAfter call), not just
// how bad the worst moment was.
func (t Timeline) PeakDepthAt() (peak int, atMs float64) {
	depth := 0
	for _, e := range t.Events {
		depth += e.Delta
		if depth > peak {
			peak = depth
			atMs = e.TimeMs
		}
	}
	return peak, atMs
}

// DrainedAfter returns the first time at or after afterMs that depth/
// capacity returns to at or below 1.0 -- generalizing the drain-check
// AnalyzeDiversion already performs (there, only reachable once a
// diversion has been found) into a standalone method usable for a
// target whose policy never diverts at all (e.g. weighted-round-robin's
// static allocation): DrainedAfter answers "did this target's queue
// ever clear," independent of whether any reactive correction occurred.
func (t Timeline) DrainedAfter(capacity int, afterMs float64) (drainAtMs float64, found bool) {
	depth := 0
	for _, e := range t.Events {
		depth += e.Delta
		if e.TimeMs >= afterMs && pressureRatio(depth, capacity) <= 1.0 {
			return e.TimeMs, true
		}
	}
	return 0, false
}

// AreaUnderCurve integrates depth over [0, horizonMs] -- the "queue
// integral" the assignment names directly (Section 7): a single number
// capturing both HOW deep the queue got and HOW LONG it stayed there,
// which peak depth alone cannot distinguish (a brief deep spike and a
// long shallow queue can share the same peak).
func (t Timeline) AreaUnderCurve(horizonMs float64) float64 {
	if horizonMs <= 0 {
		return 0
	}
	area := 0.0
	depth := 0
	lastT := 0.0
	for _, e := range t.Events {
		if e.TimeMs > horizonMs {
			break
		}
		area += float64(depth) * (e.TimeMs - lastT)
		depth += e.Delta
		lastT = e.TimeMs
	}
	area += float64(depth) * (horizonMs - lastT)
	return area
}

// pressureRatio returns depth/capacity, treating capacity<=0 (the flat,
// unlimited-capacity model) as capacity=1 -- matching
// internal/replay/world.go's own convention (capacities() defaults an
// unset/non-positive Capacity to a single always-available slot) so a
// flat-model target's ratio is directly comparable to a finite-capacity
// one.
func pressureRatio(depth, capacity int) float64 {
	if capacity <= 0 {
		capacity = 1
	}
	return float64(depth) / float64(capacity)
}

// TimeAboveThreshold returns how many milliseconds, within [0,
// horizonMs], the target's depth/capacity ratio strictly exceeds
// ratioThreshold -- M3's "time above threshold" candidate. Deliberately
// parameterized rather than hardcoded to one threshold, so a caller can
// (and, per Stage 15's own anti-fishing rule, must) check robustness
// across several candidate thresholds rather than reporting only
// whichever one looks cleanest.
func (t Timeline) TimeAboveThreshold(capacity int, ratioThreshold, horizonMs float64) float64 {
	if horizonMs <= 0 {
		return 0
	}
	total := 0.0
	depth := 0
	lastT := 0.0
	for _, e := range t.Events {
		if e.TimeMs > horizonMs {
			break
		}
		if pressureRatio(depth, capacity) > ratioThreshold {
			total += e.TimeMs - lastT
		}
		depth += e.Delta
		lastT = e.TimeMs
	}
	if pressureRatio(depth, capacity) > ratioThreshold {
		total += horizonMs - lastT
	}
	return total
}

// FractionAboveThreshold is TimeAboveThreshold normalized by horizonMs.
func (t Timeline) FractionAboveThreshold(capacity int, ratioThreshold, horizonMs float64) float64 {
	if horizonMs <= 0 {
		return 0
	}
	return t.TimeAboveThreshold(capacity, ratioThreshold, horizonMs) / horizonMs
}

// ConcentrationMetrics captures M1: how unevenly a policy's completed
// traffic is spread across targets, independent of any pressure or
// timing measure.
type ConcentrationMetrics struct {
	Top1Share   float64
	Top3Share   float64
	EntropyBits float64
}

// ComputeConcentration generalizes the topKShare/entropyBits helpers
// duplicated across cmd/experiment-014a through 014f into one shared,
// tested implementation.
func ComputeConcentration(completedByTarget map[string]int) ConcentrationMetrics {
	total := 0
	counts := make([]int, 0, len(completedByTarget))
	for _, c := range completedByTarget {
		total += c
		counts = append(counts, c)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(counts)))

	share := func(k int) float64 {
		if total == 0 {
			return 0
		}
		sum := 0
		for i := 0; i < k && i < len(counts); i++ {
			sum += counts[i]
		}
		return float64(sum) / float64(total)
	}

	entropy := 0.0
	if total > 0 {
		for _, c := range counts {
			if c == 0 {
				continue
			}
			p := float64(c) / float64(total)
			entropy -= p * math.Log2(p)
		}
	}

	return ConcentrationMetrics{Top1Share: share(1), Top3Share: share(3), EntropyBits: entropy}
}

// CongestionConfig defines, explicitly and in advance (per Stage 15's
// own anti-threshold-fishing rule -- Section 30 of the assignment),
// what counts as "congested" and what counts as a "material" diversion
// away from a congested target.
type CongestionConfig struct {
	// RatioThreshold: a target is "congested" once depth/capacity
	// strictly exceeds this value. Candidate values tested for
	// robustness elsewhere (Stage 15's predictor-search experiments):
	// 0.9 and 1.0.
	RatioThreshold float64
	// DiversionWindow: number of trailing dispatch decisions (across
	// ALL targets, not just the congested one) inspected when checking
	// whether the policy has materially diverted away from the
	// congested target. A single one-off decision to another target is
	// not "diversion" -- it must hold across a window, to distinguish a
	// genuine, sustained correction from ordinary cold-start exploration
	// or round-robin cycling.
	DiversionWindow int
	// DiversionShareThreshold: diversion is recognized once the
	// congested target's own share of dispatches within the trailing
	// DiversionWindow decisions (evaluated starting from the first
	// decision at or after congestion onset) drops below this fraction.
	DiversionShareThreshold float64
}

// FindFirstCongestionOnset returns the first time (at or after afterMs)
// that target's depth/capacity ratio strictly exceeds ratioThreshold.
// This is the right choice when a target only ever has ONE congestion
// episode; when a target can cycle through multiple build/drain
// episodes in one run (observed directly in Stage 15's own canonical
// scenario -- a policy can have a brief, inconsequential early episode
// followed by a much larger one during a workload's actual peak), the
// FIRST episode is not necessarily the one that determines the run's
// worst outcome -- see FindPeakEpisodeCongestionOnset.
func FindFirstCongestionOnset(timeline Timeline, capacity int, ratioThreshold, afterMs float64) (onsetMs float64, found bool) {
	depth := 0
	for _, e := range timeline.Events {
		depth += e.Delta
		if e.TimeMs >= afterMs && pressureRatio(depth, capacity) > ratioThreshold {
			return e.TimeMs, true
		}
	}
	return 0, false
}

// FindPeakEpisodeCongestionOnset returns the onset of whichever
// congestion episode contains the target's PEAK depth -- the episode
// responsible for the run's worst outcome, which is not always the
// first episode a target experiences (a policy can have a small, early,
// self-resolving congestion blip well before a workload's real peak
// creates its dominant collapse). An "episode" is a maximal contiguous
// stretch of time during which depth/capacity stays above
// ratioThreshold; onset is the instant depth/capacity first exceeded
// ratioThreshold at the start of that specific stretch.
func FindPeakEpisodeCongestionOnset(timeline Timeline, capacity int, ratioThreshold float64) (onsetMs float64, found bool) {
	depth, peak := 0, 0
	inEpisode := false
	var episodeStart, peakEpisodeStart float64
	peakFound := false
	for _, e := range timeline.Events {
		depth += e.Delta
		ratio := pressureRatio(depth, capacity)
		if ratio > ratioThreshold {
			if !inEpisode {
				inEpisode = true
				episodeStart = e.TimeMs
			}
			if depth > peak {
				peak = depth
				peakEpisodeStart = episodeStart
				peakFound = true
			}
		} else {
			inEpisode = false
		}
	}
	return peakEpisodeStart, peakFound
}

// DiversionResult is one target's full congestion -> commitment ->
// diversion -> drain timeline under one CongestionConfig.
type DiversionResult struct {
	CongestionAtMs   float64
	CongestionFound  bool
	DiversionAtMs    float64
	DiversionFound   bool
	CommittedBacklog int // dispatches to target in [CongestionAtMs, DiversionAtMs) -- Section 10's operational definition, applied literally
	QueueDrainAtMs   float64
	QueueDrainFound  bool
}

// AnalyzeDiversion computes M4 (committed backlog) and M5 (unlock/
// diversion dynamics) for one target, given an ALREADY-DETERMINED
// congestion onset time (from FindFirstCongestionOnset or
// FindPeakEpisodeCongestionOnset -- the caller decides which episode
// matters, since a target can have more than one across a run).
//
// Diversion is the first time, at or after congestionAtMs, that a
// trailing window of cfg.DiversionWindow dispatch decisions (drawn from
// ALL of records, so it reflects the policy's ACTUAL routing mix, not
// just this target's own activity) shows target's own share below cfg.
// DiversionShareThreshold. Committed backlog is the literal count of
// dispatches TO target during [congestionAtMs, diversion) -- Section
// 10's definition applied without reinterpretation. Queue drain is the
// first time at or after diversion that target's depth/capacity ratio
// returns to at or below 1.0 (fully caught up, not merely "improving").
func AnalyzeDiversion(records []replay.SelectionRecord, timeline Timeline, target string, capacity int, congestionAtMs float64, congestionFound bool, cfg CongestionConfig, horizonMs float64) DiversionResult {
	var result DiversionResult
	result.CongestionAtMs, result.CongestionFound = congestionAtMs, congestionFound
	if !result.CongestionFound {
		return result
	}

	// Diversion: scan forward through ALL records (every target) from
	// congestion onset, evaluating target's own share in each trailing
	// window of cfg.DiversionWindow decisions.
	sorted := make([]replay.SelectionRecord, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].VirtualTimeMs < sorted[j].VirtualTimeMs })

	startIdx := -1
	for i, r := range sorted {
		if r.VirtualTimeMs >= result.CongestionAtMs {
			startIdx = i
			break
		}
	}
	if startIdx == -1 || cfg.DiversionWindow <= 0 {
		return result
	}
	for i := startIdx; i+cfg.DiversionWindow <= len(sorted); i++ {
		window := sorted[i : i+cfg.DiversionWindow]
		toTarget := 0
		for _, r := range window {
			if r.Target == target {
				toTarget++
			}
		}
		share := float64(toTarget) / float64(cfg.DiversionWindow)
		if share < cfg.DiversionShareThreshold {
			result.DiversionAtMs = window[len(window)-1].VirtualTimeMs
			result.DiversionFound = true
			break
		}
	}
	if !result.DiversionFound {
		return result
	}

	// Committed backlog: dispatches to target strictly within
	// [CongestionAtMs, DiversionAtMs).
	for _, r := range records {
		if r.Target == target && r.VirtualTimeMs >= result.CongestionAtMs && r.VirtualTimeMs < result.DiversionAtMs {
			result.CommittedBacklog++
		}
	}

	// Queue drain: first time at or after diversion that depth/capacity
	// returns to <= 1.0.
	depth := 0
	for _, e := range timeline.Events {
		depth += e.Delta
		if e.TimeMs >= result.DiversionAtMs && pressureRatio(depth, capacity) <= 1.0 {
			result.QueueDrainAtMs = e.TimeMs
			result.QueueDrainFound = true
			break
		}
	}
	return result
}
