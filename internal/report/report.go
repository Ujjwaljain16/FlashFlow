// Package report turns internal/backlog's already-computed mechanism
// data into diagnostic tooling: a classifier that labels a policy's
// behavior on one scenario (STABLE / ACUTE_COLLAPSE / CHRONIC_COLLAPSE /
// RECOVERY_LIMITED), a structured report format, and a causal-narrative
// "explain" renderer with counterfactual comparisons against other
// policies from the same run.
//
// This is diagnostic tooling built ON TOP of Stages 11-16's completed
// research, not a new research question: every quantity here already
// existed in internal/backlog or Stage 16's own published findings.
// Nothing here introduces a new topology model, a new experiment
// scenario shape, or a new research claim -- see
// docs/StageArtifacts/Stage17-DiagnosticTooling.md for the classifier's
// decision tree and where its two magnitude constants come from.
package report

import (
	"fmt"

	"flashflow/internal/backlog"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
)

// Classification labels the shape of a target's failure, per Stage
// 15/16's own two-mechanism model (docs/StageArtifacts/Stage15.md,
// Stage16.md).
type Classification string

const (
	Stable          Classification = "STABLE"
	AcuteCollapse   Classification = "ACUTE_COLLAPSE"
	ChronicCollapse Classification = "CHRONIC_COLLAPSE"
	RecoveryLimited Classification = "RECOVERY_LIMITED"
)

// Two magnitude constants the classifier needs. Both were calibrated
// iteratively against the flagship's own six already-published policy
// outcomes -- not derived from an independent theoretical argument, and
// not validated against any held-out scenario or policy outside that
// same set of six. An earlier version of this comment (and of
// docs/StageArtifacts/Stage17-DiagnosticTooling.md) described these
// values as "chosen once, up front... not tuned after the fact," which
// an independent audit correctly identified as inconsistent with the
// classifier's own documented development history immediately below
// (Bug 1/Bug 3 in the diagnostic-tooling doc are, precisely, instances
// of adjusting the tree until it matched these six known outcomes). Both
// constants sit in the gap between round-robin's own value and every
// other policy's -- a real, principled discriminator for THIS
// calibration set, but one small, fixed sample, not a law. See
// docs/StageArtifacts/Stage17-DiagnosticTooling.md for the corrected
// account and its stated generalization caveat.
const (
	// concentrationFairShareMultiple: a target is treated as genuinely
	// CONCENTRATED-upon (as opposed to merely receiving its ordinary,
	// unremarkable fair share) once its completed-request share exceeds
	// this multiple of 1/targetCount. Validated directly against the
	// flagship's own six policies: round-robin's bottleneck sits at
	// 1.06x fair share (i.e. it never meaningfully concentrates at
	// all -- overload there is a pure allocation mismatch), while every
	// other policy's own bottleneck sits at 1.4x-3.1x fair share. This
	// is the discriminator that correctly separates round-robin's
	// CHRONIC failure (never concentrates, permanently over capacity)
	// from EWMA/Adaptive's ACUTE failure (concentrates hard, then
	// spends a long time draining what it built) -- an earlier version
	// of this classifier used fraction-of-time-over-capacity as the
	// primary chronic/acute gate instead, and that misclassified EWMA
	// as RecoveryLimited, because a slow-to-drain ACUTE episode can
	// spend just as much of the horizon over capacity as a genuinely
	// CHRONIC one; concentration, not time-over-capacity, is what
	// actually distinguishes the two mechanisms.
	concentrationFairShareMultiple = 1.2
	// acuteBacklogCapacityMultiple: among genuinely concentrated-and-
	// drained cases, an episode that only resolves after committing at
	// least this many multiples of the target's own capacity is still
	// classified as an acute collapse, not "stable," since the eventual
	// recovery came too late to spare the accumulated tail latency.
	acuteBacklogCapacityMultiple = 10
)

// Metrics is everything the classifier and the report renderers need
// about one policy's behavior on its own bottleneck target, for one
// run. Every field is computed from data internal/backlog already
// exposes; nothing here is a new measurement.
type Metrics struct {
	MeanMs             float64 `json:"mean_ms"` // whole-run outcome, not a backlog/mechanism quantity, but the headline number every comparison view needs alongside the mechanism classification
	P99Ms              float64 `json:"p99_ms"`
	Bottleneck         string  `json:"bottleneck"`
	Capacity           int     `json:"capacity"`
	PeakDepth          int     `json:"peak_depth"`
	PeakDepthAtMs      float64 `json:"peak_depth_at_ms"`
	ConcentrationRatio float64 `json:"concentration_ratio"` // bottleneck's own completed share, divided by 1/targetCount (fair share)
	// Concentrated is ConcentrationRatio >= concentrationFairShareMultiple,
	// computed once here so Classify, the CLI's rendered narrative, and
	// the dashboard's own JS narrative all read the SAME boolean instead
	// of each re-deriving it (or, worse, hardcoding the 1.2 threshold a
	// second time in JS, where it could silently drift from this
	// package's own value).
	Concentrated          bool    `json:"concentrated"`
	CongestionFound       bool    `json:"congestion_found"`
	FirstCongestionMs     float64 `json:"first_congestion_ms"`
	DiversionFound        bool    `json:"diversion_found"`
	FirstDiversionMs      float64 `json:"first_diversion_ms"`
	Drained               bool    `json:"drained"`
	DrainAtMs             float64 `json:"drain_at_ms"`
	CommittedWork         int     `json:"committed_work"`
	TimeAboveCapacityMs   float64 `json:"time_above_capacity_ms"`
	FractionAboveCapacity float64 `json:"fraction_above_capacity"`
}

// AnalyzeTarget is the reusable per-run computation: finds the
// bottleneck target (highest peak depth, matching experiment-016-
// flagship's own convention), then computes every Metrics field from
// internal/backlog's own exported API.
func AnalyzeTarget(wr *replay.WorldResult, targets []replay.TargetProfile, capacity int, horizonMs float64, cfg backlog.CongestionConfig) Metrics {
	bottleneck, peak, peakAt := "", -1, 0.0
	for _, t := range targets {
		tl := backlog.BuildTimeline(wr.Records, wr.Completions, t.Name)
		p, at := tl.PeakDepthAt()
		if p > peak {
			bottleneck, peak, peakAt = t.Name, p, at
		}
	}

	m := Metrics{Bottleneck: bottleneck, Capacity: capacity, PeakDepth: peak, PeakDepthAtMs: peakAt}
	if len(wr.Completions) > 0 {
		latenciesMs := make([]float64, len(wr.Completions))
		for i, c := range wr.Completions {
			latenciesMs[i] = float64(c.Latency.Microseconds()) / 1000.0
		}
		m.MeanMs, _ = statistics.Mean(latenciesMs)
		m.P99Ms, _ = statistics.Percentile(latenciesMs, 99)
	}
	tl := backlog.BuildTimeline(wr.Records, wr.Completions, bottleneck)
	m.FractionAboveCapacity = tl.FractionAboveThreshold(capacity, cfg.RatioThreshold, horizonMs)
	m.TimeAboveCapacityMs = tl.TimeAboveThreshold(capacity, cfg.RatioThreshold, horizonMs)

	onset, onsetFound := backlog.FindPeakEpisodeCongestionOnset(tl, capacity, cfg.RatioThreshold)
	m.CongestionFound, m.FirstCongestionMs = onsetFound, onset
	if !onsetFound {
		return m // never congested: everything below stays at its zero value, correctly meaning N/A
	}

	dr := backlog.AnalyzeDiversion(wr.Records, tl, bottleneck, capacity, onset, onsetFound, cfg, horizonMs)
	m.DiversionFound, m.FirstDiversionMs = dr.DiversionFound, dr.DiversionAtMs

	var endMs float64
	if dr.DiversionFound {
		endMs = dr.DiversionAtMs
		m.Drained, m.DrainAtMs = dr.QueueDrainFound, dr.QueueDrainAtMs
	} else {
		// No diversion was ever detected -- e.g. a static policy whose
		// share never crosses the diversion threshold at all. Whether
		// the target recovers is then a property of the TARGET alone
		// (does its own queue drain), not of any policy reaction.
		drainAt, drained := tl.DrainedAfter(capacity, peakAt)
		m.Drained, m.DrainAtMs = drained, drainAt
		if drained {
			endMs = drainAt
		} else {
			endMs = horizonMs
		}
	}
	m.CommittedWork = committedWork(wr.Records, bottleneck, onset, endMs)

	// Concentration is measured DURING the congestion episode itself
	// ([onset, endMs)), not across the target's whole-run dispatch
	// share -- a real discrepancy found while building this classifier:
	// EWMA's own bottleneck target (worst PEAK depth) is not always the
	// same target it favors for MOST of the run. It can lock onto one
	// fast target for the low-rate baseline period, then briefly prefer
	// a DIFFERENT target during a sudden burst (before its smoothed
	// signal catches up), overwhelming that second target even though
	// its OWN whole-run dispatch share stays modest. Whole-run
	// concentration would have missed this episode entirely; episode-
	// windowed concentration correctly captures it.
	m.ConcentrationRatio = concentrationRatioInWindow(wr.Records, bottleneck, len(targets), onset, endMs)
	m.Concentrated = m.ConcentrationRatio >= concentrationFairShareMultiple
	return m
}

// concentrationRatioInWindow is the bottleneck's own DISPATCHED-request
// share (not its completed share -- deliberately, see below) WITHIN
// [fromMs, toMs), divided by its fair share (1/targetCount): 1.0 means
// it received exactly its fair share during that window (no
// concentration at all); higher means a policy is genuinely funneling
// more than its even split onto this one target during the episode
// being evaluated.
//
// Windowed, not whole-run: a target's OWN worst congestion episode is
// not always aligned with which target a policy favors for MOST of the
// run (see AnalyzeTarget's own comment) -- measuring only the episode
// window correctly attributes concentration to the event actually being
// classified.
//
// Dispatches, not completions: a slow, still-backlogged target can show
// a LOWER completed count than it was actually sent, since many of its
// dispatched requests are still queued or in flight at the end of the
// window. Using completions would silently undercount exactly the
// targets this classifier most needs to recognize as concentrated --
// the same horizon-truncation distortion Stage 13 already found and
// fixed in internal/attribution's own rho calculation; caught here
// before being shipped rather than after.
func concentrationRatioInWindow(records []replay.SelectionRecord, bottleneck string, targetCount int, fromMs, toMs float64) float64 {
	if targetCount <= 0 {
		return 0
	}
	total, bottleneckCount := 0, 0
	for _, r := range records {
		if r.VirtualTimeMs < fromMs || r.VirtualTimeMs >= toMs {
			continue
		}
		total++
		if r.Target == bottleneck {
			bottleneckCount++
		}
	}
	if total == 0 {
		return 0
	}
	share := float64(bottleneckCount) / float64(total)
	fairShare := 1.0 / float64(targetCount)
	return share / fairShare
}

// committedWork counts dispatches to target strictly within
// [fromMs, toMs) -- the generalization of DiversionResult.CommittedBacklog
// that also covers the "never diverted" case (see AnalyzeTarget above).
func committedWork(records []replay.SelectionRecord, target string, fromMs, toMs float64) int {
	count := 0
	for _, r := range records {
		if r.Target == target && r.VirtualTimeMs >= fromMs && r.VirtualTimeMs < toMs {
			count++
		}
	}
	return count
}

// Classify applies the decision tree documented in
// docs/StageArtifacts/Stage17-DiagnosticTooling.md, returning both the
// classification and a short, human-readable reason.
//
// The tree is deliberately SYMMETRIC in COMMITTED WORK first (did this
// episode actually accumulate meaningful backlog, regardless of why),
// then uses CONCENTRATION only to name the mechanism once severity is
// established -- an earlier, asymmetric version of this tree checked
// committed work only on the "concentrated" branch, which
// misclassified P2C-load (which never concentrates by design, and
// accumulates almost no backlog, committed_work=4) as RecoveryLimited
// instead of Stable: not concentrating and having negligible backlog
// are both good outcomes and must both route to Stable, not just the
// concentrated-and-small-backlog case.
func Classify(m Metrics) (Classification, string) {
	if !m.CongestionFound {
		return Stable, "the target never exceeded its own capacity"
	}
	concentrated := m.ConcentrationRatio >= concentrationFairShareMultiple
	capacity := m.Capacity
	if capacity <= 0 {
		capacity = 1
	}
	backlogRatio := float64(m.CommittedWork) / float64(capacity)
	severe := backlogRatio >= acuteBacklogCapacityMultiple

	if !m.Drained {
		if concentrated {
			return AcuteCollapse, fmt.Sprintf("concentrated %.1fx its fair share and never resolved within the observed horizon", m.ConcentrationRatio)
		}
		return ChronicCollapse, fmt.Sprintf("received only %.1fx its fair share (never genuinely concentrated) and never drained -- a permanent allocation mismatch, not a one-time event", m.ConcentrationRatio)
	}

	if !severe {
		return Stable, fmt.Sprintf("committed only %d requests (%.1fx capacity) before the episode resolved, regardless of concentration (%.1fx fair share)", m.CommittedWork, backlogRatio, m.ConcentrationRatio)
	}
	if concentrated {
		return AcuteCollapse, fmt.Sprintf("concentrated %.1fx its fair share and committed %d requests (%.0fx capacity) before the episode resolved", m.ConcentrationRatio, m.CommittedWork, backlogRatio)
	}
	return RecoveryLimited, fmt.Sprintf("received only %.1fx its fair share (never genuinely concentrated) yet still committed %d requests (%.0fx capacity) before eventually draining -- a structural mismatch, not an active correction", m.ConcentrationRatio, m.CommittedWork, backlogRatio)
}

// policyMechanism names each policy's own information source and
// typical mechanism, mirroring docs/StageArtifacts/Stage16.md's
// "Policy Mechanisms" table verbatim rather than inventing new
// terminology.
var policyMechanism = map[string]string{
	"round-robin":          "NO-ADAPTATION FIXED ALLOCATION",
	"weighted-round-robin": "STATIC CAPACITY-AWARE ALLOCATION",
	"least-connections":    "CURRENT-PRESSURE ANTI-CONCENTRATION",
	"ewma":                 "SMOOTHED-HISTORY LOCK-IN",
	"p2c-load":             "SAMPLING-BOUNDED CONCENTRATION",
	"adaptive":             "MULTI-SIGNAL (LOAD-PROTECTED)",
}

// Mechanism returns the policy's own named mechanism, or a generic
// fallback for a policy name this table doesn't recognize (keeps the
// package usable if a caller passes an unexpected policy name rather
// than panicking or silently returning an empty string).
func Mechanism(policy string) string {
	if m, ok := policyMechanism[policy]; ok {
		return m
	}
	return "UNCLASSIFIED MECHANISM"
}

// ClassificationSubtitle is a short, plain-spoken qualifier meant to be
// printed directly beside a classification label. STABLE in particular
// invites misreading as "this policy is safe" -- what the classifier
// actually means is narrower: no collapse mechanism tripped this
// decision tree's own gates on this run. Every label gets an equally
// literal qualifier so none of them implies more certainty than the
// tree actually has.
func ClassificationSubtitle(c Classification) string {
	switch c {
	case Stable:
		return "no collapse mechanism detected under current diagnostic criteria"
	case AcuteCollapse:
		return "concentrated overload that had not, or had only just, resolved"
	case ChronicCollapse:
		return "permanent allocation mismatch; the policy never adapts"
	case RecoveryLimited:
		return "recovered, but from the workload easing, not from any active correction"
	default:
		return ""
	}
}

// Interpretation renders one sentence combining the policy's own
// mechanism with what actually happened, in the same spirit as Stage
// 16's own prose explanations.
func Interpretation(policy string, class Classification, reason string) string {
	switch class {
	case Stable:
		return fmt.Sprintf("%s kept its bottleneck target within a recoverable range (%s).", policy, reason)
	case AcuteCollapse:
		return fmt.Sprintf("%s continued committing traffic to a target after its actual capacity state had deteriorated (%s).", policy, reason)
	case ChronicCollapse:
		return fmt.Sprintf("%s never adapted its own allocation, so the target stayed permanently over capacity (%s).", policy, reason)
	case RecoveryLimited:
		return fmt.Sprintf("%s never actively corrected; the target only recovered because the workload itself eased (%s).", policy, reason)
	default:
		return reason
	}
}
