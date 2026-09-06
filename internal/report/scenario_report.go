package report

import (
	"fmt"
	"strings"

	"flashflow/internal/backlog"
	"flashflow/internal/replay"
)

// PolicyReport is one policy's classified behavior on one scenario,
// aggregated across however many seeds it was run with.
type PolicyReport struct {
	Policy         string         `json:"policy"`
	Metrics        Metrics        `json:"metrics"`
	Classification Classification `json:"classification"`
	Reason         string         `json:"reason"`
	Mechanism      string         `json:"mechanism"`
	SeedMetrics    []Metrics      `json:"seed_metrics"`
	Confidence     string         `json:"confidence"`
}

// ScenarioReport is every policy's PolicyReport for the SAME scenario
// and seed set -- the unit "explain" reads back to build its
// counterfactual comparison, entirely from data already computed here
// (no rerun, no raw records needed beyond what was already collected).
type ScenarioReport struct {
	ScenarioLabel string                 `json:"scenario_label"`
	Targets       []replay.TargetProfile `json:"targets"`
	Capacity      int                    `json:"capacity"`
	HorizonMs     float64                `json:"horizon_ms"`
	Policies      []PolicyReport         `json:"policies"`
}

// BuildScenarioReport runs AnalyzeTarget+Classify for every seed of
// every policy in policyOrder, picks the majority classification per
// policy, and records a representative Metrics/reason (the first seed
// whose own classification matches the majority) plus a confidence
// string.
func BuildScenarioReport(label string, targets []replay.TargetProfile, capacity int, horizonMs float64, cfg backlog.CongestionConfig, perPolicySeedResults map[string][]*replay.WorldResult, policyOrder []string) ScenarioReport {
	sr := ScenarioReport{ScenarioLabel: label, Targets: targets, Capacity: capacity, HorizonMs: horizonMs}
	for _, policy := range policyOrder {
		results := perPolicySeedResults[policy]
		if len(results) == 0 {
			continue
		}
		var seedMetrics []Metrics
		counts := map[Classification]int{}
		for _, wr := range results {
			m := AnalyzeTarget(wr, targets, capacity, horizonMs, cfg)
			seedMetrics = append(seedMetrics, m)
			c, _ := Classify(m)
			counts[c]++
		}
		dominant, dominantCount := Classification(""), 0
		for c, n := range counts {
			if n > dominantCount {
				dominant, dominantCount = c, n
			}
		}
		primary, reason := seedMetrics[0], ""
		for _, m := range seedMetrics {
			if c, r := Classify(m); c == dominant {
				primary, reason = m, r
				break
			}
		}
		sr.Policies = append(sr.Policies, PolicyReport{
			Policy: policy, Metrics: primary, Classification: dominant, Reason: reason,
			Mechanism:   Mechanism(policy),
			SeedMetrics: seedMetrics,
			Confidence:  fmt.Sprintf("%d/%d seeded replication", dominantCount, len(results)),
		})
	}
	return sr
}

// FindPolicy returns the named policy's PolicyReport, or false if this
// ScenarioReport has none by that name.
func (sr ScenarioReport) FindPolicy(policy string) (PolicyReport, bool) {
	for _, p := range sr.Policies {
		if p.Policy == policy {
			return p, true
		}
	}
	return PolicyReport{}, false
}

func formatSeconds(ms float64) string {
	return fmt.Sprintf("%.3fs", ms/1000.0)
}

// RenderText renders the named policy's PolicyReport in the plain,
// divider-based format this project's own style favors over decorative
// box-drawing -- the data doesn't need a fancier presentation than a
// horizontal rule to be legible.
func (sr ScenarioReport) RenderText(policy string) string {
	pr, ok := sr.FindPolicy(policy)
	if !ok {
		return fmt.Sprintf("no report found for policy %q in this scenario report", policy)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "FLASHFLOW FAILURE REPORT")
	fmt.Fprintln(&b, strings.Repeat("-", 39))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Scenario")
	fmt.Fprintln(&b, sr.ScenarioLabel)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Policy: %s\n\n", pr.Policy)
	fmt.Fprintln(&b, "Failure classification")
	fmt.Fprintln(&b, string(pr.Classification))
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%-28s %d\n", "Peak queue", pr.Metrics.PeakDepth)
	fmt.Fprintf(&b, "%-28s %d\n", "Committed work", pr.Metrics.CommittedWork)
	fmt.Fprintf(&b, "%-28s %s\n", "Time above capacity", formatSeconds(pr.Metrics.TimeAboveCapacityMs))
	if pr.Metrics.CongestionFound {
		fmt.Fprintf(&b, "%-28s %s\n", "First congestion", formatSeconds(pr.Metrics.FirstCongestionMs))
	} else {
		fmt.Fprintf(&b, "%-28s %s\n", "First congestion", "never")
	}
	if pr.Metrics.DiversionFound {
		fmt.Fprintf(&b, "%-28s %s\n", "First material diversion", formatSeconds(pr.Metrics.FirstDiversionMs))
	} else {
		fmt.Fprintf(&b, "%-28s %s\n", "First material diversion", "never (no reactive correction detected)")
	}
	if pr.Metrics.Drained {
		fmt.Fprintf(&b, "%-28s %s\n", "Queue drained", formatSeconds(pr.Metrics.DrainAtMs))
	} else {
		fmt.Fprintf(&b, "%-28s %s\n", "Queue drained", "never (within observed horizon)")
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Primary mechanism")
	fmt.Fprintln(&b, pr.Mechanism)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Interpretation")
	fmt.Fprintln(&b, Interpretation(pr.Policy, pr.Classification, pr.Reason))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Confidence")
	fmt.Fprintln(&b, pr.Confidence)
	return b.String()
}

// RenderExplanation produces the numbered causal narrative plus a
// counterfactual comparison against every other policy in this SAME
// ScenarioReport -- entirely from fields already computed by
// BuildScenarioReport, so "explain" never needs to rerun the scenario
// or read raw per-request records.
func (sr ScenarioReport) RenderExplanation(policy string) string {
	pr, ok := sr.FindPolicy(policy)
	if !ok {
		return fmt.Sprintf("no report found for policy %q in this scenario report", policy)
	}
	var b strings.Builder
	fmt.Fprintln(&b, "WHY DID THIS POLICY COLLAPSE?")
	fmt.Fprintln(&b)

	if !pr.Metrics.CongestionFound {
		fmt.Fprintf(&b, "1. %s's bottleneck target (%s) never exceeded its own capacity.\n", pr.Policy, pr.Metrics.Bottleneck)
		fmt.Fprintf(&b, "\nResult classified as %s.\n", pr.Classification)
		return b.String()
	}

	step := 1
	numbered := func(format string, args ...any) {
		fmt.Fprintf(&b, "%d. %s\n", step, fmt.Sprintf(format, args...))
		step++
	}
	numbered("Traffic concentrated on %s.", pr.Metrics.Bottleneck)
	numbered("%s crossed capacity at %s.", pr.Metrics.Bottleneck, formatSeconds(pr.Metrics.FirstCongestionMs))
	if pr.Metrics.DiversionFound {
		numbered("%d additional requests were committed before diversion.", pr.Metrics.CommittedWork)
		numbered("The policy diverted new traffic away at %s.", formatSeconds(pr.Metrics.FirstDiversionMs))
	} else {
		numbered("The policy never diverted new traffic away from %s at all.", pr.Metrics.Bottleneck)
	}
	numbered("Queue depth reached %d in-flight requests.", pr.Metrics.PeakDepth)
	numbered("The target spent %.0f%% of the observed horizon over capacity.", pr.Metrics.FractionAboveCapacity*100)
	if pr.Metrics.Drained {
		numbered("The queue eventually drained at %s.", formatSeconds(pr.Metrics.DrainAtMs))
	} else {
		numbered("The queue never drained within the observed horizon.")
	}
	numbered("Result classified as %s.", string(pr.Classification))

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Most likely mechanism:")
	fmt.Fprintln(&b, pr.Mechanism)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Counterfactual (same scenario, same seeds, different policy):")
	for _, other := range sr.Policies {
		if other.Policy == policy {
			continue
		}
		fmt.Fprintf(&b, "  %-22s committed_work=%-5d classification=%s\n", other.Policy, other.Metrics.CommittedWork, other.Classification)
	}
	return b.String()
}
