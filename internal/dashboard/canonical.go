// canonical.go is the dashboard's glue to internal/report's canonical
// scenario and classifier -- the "Control Room" view's own backend,
// kept separate from playground.go's PlaygroundScenario/PolicyByName
// (a different, older 3-target topology `report`'s classifier was
// never validated against) so neither path disturbs the other.
package dashboard

import (
	"fmt"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
	"flashflow/internal/report"
)

// RunCanonicalReport is a thin wrapper over report.RunCanonicalScenario,
// giving the dashboard's HTTP handler a stable, dashboard-local entry
// point consistent with this package's existing Run*/Compare* naming
// (RunPlayground, ComparePlayground).
func RunCanonicalReport(seedCount int) (report.ScenarioReport, error) {
	return report.RunCanonicalScenario(seedCount)
}

// DivergenceSummary is CompareSummary's canonical-scenario analog: two
// policies run against the byte-for-byte identical canonical scenario/
// seed, their first point of trace divergence (the same
// replay.FirstDivergence every counterfactual experiment since Stage 7
// has used -- see ComparePlayground in playground.go for the identical
// pattern against the older Playground scenario), and each side's own
// report.PolicyReport for the mechanism explanation the divergence view
// also shows.
type DivergenceSummary struct {
	Seed                  int64                    `json:"seed"`
	Baseline              report.PolicyReport      `json:"baseline"`
	Counterfactual        report.PolicyReport      `json:"counterfactual"`
	BaselineRecords       []replay.SelectionRecord `json:"baseline_records"`
	CounterfactualRecords []replay.SelectionRecord `json:"counterfactual_records"`
	Diverged              bool                     `json:"diverged"`
	DivergenceIndex       int                      `json:"divergence_index"`
	DivergenceTimeMs      float64                  `json:"divergence_time_ms"`
}

func canonicalScenario(seed int64) (replay.Scenario, []replay.TargetProfile) {
	targets := report.CanonicalTargets()
	arrivals, seeds := report.CanonicalArrivals(seed, 0.3)
	scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(report.CanonicalHorizon.Nanoseconds()), Seeds: seeds}
	return scenario, targets
}

func classifyRun(policy string, result *replay.WorldResult, targets []replay.TargetProfile) report.PolicyReport {
	horizonMs := float64(report.CanonicalHorizon.Milliseconds())
	metrics := report.AnalyzeTarget(result, targets, report.CanonicalCapacity, horizonMs, report.CanonicalCongestionConfig())
	class, reason := report.Classify(metrics)
	return report.PolicyReport{Policy: policy, Metrics: metrics, Classification: class, Reason: reason, Mechanism: report.Mechanism(policy)}
}

// CompareCanonical runs baselinePolicy and counterfactualPolicy against
// the identical canonical scenario/seed and reports both their
// mechanism classification and their first point of divergence.
func CompareCanonical(baselinePolicy, counterfactualPolicy string, seed int64) (DivergenceSummary, error) {
	baselineSpec, err := report.PolicyByName(baselinePolicy)
	if err != nil {
		return DivergenceSummary{}, err
	}
	cfSpec, err := report.PolicyByName(counterfactualPolicy)
	if err != nil {
		return DivergenceSummary{}, err
	}

	scenario, targets := canonicalScenario(seed)
	baselineResult, err := replay.RunWorld(scenario, baselineSpec)
	if err != nil {
		return DivergenceSummary{}, fmt.Errorf("dashboard: running canonical baseline: %w", err)
	}
	cfResult, err := replay.RunWorld(scenario, cfSpec)
	if err != nil {
		return DivergenceSummary{}, fmt.Errorf("dashboard: running canonical counterfactual: %w", err)
	}

	idx, diverged := replay.FirstDivergence(baselineResult.Trace, cfResult.Trace)
	summary := DivergenceSummary{
		Seed:                  seed,
		Baseline:              classifyRun(baselinePolicy, &baselineResult, targets),
		Counterfactual:        classifyRun(counterfactualPolicy, &cfResult, targets),
		BaselineRecords:       baselineResult.Records,
		CounterfactualRecords: cfResult.Records,
		Diverged:              diverged,
		DivergenceIndex:       idx,
	}
	if diverged && idx < len(baselineResult.Trace) {
		summary.DivergenceTimeMs = float64(baselineResult.Trace[idx].Time) / 1e6
	}
	return summary, nil
}

// TimelineView is one policy's full event-timeline data: the overall
// traffic-rate shape, one queue-depth series per target, and that
// policy's own Metrics (its FirstCongestionMs/FirstDiversionMs/
// DrainAtMs become the timeline's marker positions).
type TimelineView struct {
	Policy       string                          `json:"policy"`
	Seed         int64                           `json:"seed"`
	Traffic      []report.SeriesPoint            `json:"traffic"`
	TargetDepths map[string][]report.SeriesPoint `json:"target_depths"`
	Metrics      report.Metrics                  `json:"metrics"`
}

// RunCanonicalTimeline runs one policy against the canonical scenario
// and returns everything the dashboard's event-timeline view needs.
func RunCanonicalTimeline(policyName string, seed int64, buckets int) (TimelineView, error) {
	spec, err := report.PolicyByName(policyName)
	if err != nil {
		return TimelineView{}, err
	}
	scenario, targets := canonicalScenario(seed)
	result, err := replay.RunWorld(scenario, spec)
	if err != nil {
		return TimelineView{}, fmt.Errorf("dashboard: running canonical timeline: %w", err)
	}

	if buckets <= 0 {
		buckets = 60
	}
	horizonMs := float64(report.CanonicalHorizon.Milliseconds())
	view := TimelineView{
		Policy:       policyName,
		Seed:         seed,
		Traffic:      report.TrafficSeries(&result, buckets, horizonMs),
		TargetDepths: make(map[string][]report.SeriesPoint, len(targets)),
	}
	for _, t := range targets {
		view.TargetDepths[t.Name] = report.DepthSeries(&result, t.Name, buckets, horizonMs)
	}
	view.Metrics = report.AnalyzeTarget(&result, targets, report.CanonicalCapacity, horizonMs, report.CanonicalCongestionConfig())
	return view, nil
}

// StressMapResult is one policy's Regime Explorer grid, with the seed
// that produced it attached so the view's own Reproduce panel doesn't
// need a second round trip to state what it just ran.
type StressMapResult struct {
	Policy string                 `json:"policy"`
	Seed   int64                  `json:"seed"`
	Cells  []report.StressMapCell `json:"cells"`
}

// RunCanonicalStressMap is a thin wrapper over report.RunStressMap,
// matching this package's existing Run*/Compare* naming.
func RunCanonicalStressMap(policyName string, seed int64) (StressMapResult, error) {
	cells, err := report.RunStressMap(policyName, seed)
	if err != nil {
		return StressMapResult{}, err
	}
	return StressMapResult{Policy: policyName, Seed: seed, Cells: cells}, nil
}
