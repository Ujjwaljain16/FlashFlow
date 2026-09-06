package report

import (
	"fmt"
	"log"
	"strings"
	"text/tabwriter"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

// PolicyByName mirrors the six policies this project has compared
// since Stage 13, keyed by their own PolicySpec.Name -- reused here so
// the stress map (and any future caller) can select a policy from a
// plain string (a CLI flag) without duplicating each policy's own
// constructor.
func PolicyByName(name string) (replay.PolicySpec, error) {
	switch name {
	case "round-robin":
		return replay.RoundRobinPolicy(), nil
	case "weighted-round-robin":
		return replay.WeightedRoundRobinPolicy(), nil
	case "least-connections":
		return replay.LeastConnectionsPolicy(), nil
	case "ewma":
		return replay.EWMAPolicy(), nil
	case "p2c-load":
		return replay.P2CLoadPolicy(), nil
	case "adaptive":
		return replay.AdaptivePolicy(), nil
	default:
		return replay.PolicySpec{}, fmt.Errorf("report: unknown policy %q (want one of round-robin, weighted-round-robin, least-connections, ewma, p2c-load, adaptive)", name)
	}
}

// PolicyNames lists every policy PolicyByName recognizes, in this
// project's own established display order.
func PolicyNames() []string {
	return []string{"round-robin", "weighted-round-robin", "least-connections", "ewma", "p2c-load", "adaptive"}
}

// heterogeneityTopologies are the exact three 3-target topologies
// experiment-013a first established and every later stage reused
// (10/15/20ms low, 10/20/40ms moderate, 15/30/60ms severe) -- not a new
// topology model, per this project's own standing constraint against
// introducing one.
func heterogeneityTopologies() []struct {
	name    string
	targets []replay.TargetProfile
} {
	build := func(msValues ...int) []replay.TargetProfile {
		out := make([]replay.TargetProfile, len(msValues))
		for i, ms := range msValues {
			out[i] = replay.TargetProfile{Name: fmt.Sprintf("edge-%02d", i), ServiceTime: time.Duration(ms) * time.Millisecond, Capacity: CanonicalCapacity}
		}
		return out
	}
	return []struct {
		name    string
		targets []replay.TargetProfile
	}{
		{"low", build(10, 15, 20)},
		{"moderate", build(10, 20, 40)},
		{"severe", build(15, 30, 60)},
	}
}

// stressMapWorkloads are the exact three workload shapes
// experiment-015e already used for its own cross-workload predictor
// test -- Requests/Horizon matched across all three so only shape, not
// total offered load, differs.
func stressMapWorkloads() []struct {
	name   string
	p      traffic.Pattern
	params traffic.Params
} {
	horizon := CanonicalHorizon
	return []struct {
		name   string
		p      traffic.Pattern
		params traffic.Params
	}{
		{"constant", traffic.Constant, traffic.Params{Requests: 600, Horizon: horizon, KeyFunc: traffic.HotColdKeys(0.5)}},
		{"burst", traffic.Burst, traffic.Params{Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300, BurstAt: 4 * time.Second, BurstWidth: 1 * time.Second, KeyFunc: traffic.HotColdKeys(0.5)}},
		{"flash_crowd", traffic.FlashCrowd, traffic.Params{Requests: 600, Horizon: horizon, BaseRate: 20, PeakRate: 300, BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond, KeyFunc: traffic.HotColdKeys(0.5)}},
	}
}

// StressMapCell is one (heterogeneity, workload) cell's classified
// outcome for one policy.
type StressMapCell struct {
	Heterogeneity  string         `json:"heterogeneity"`
	Workload       string         `json:"workload"`
	Classification Classification `json:"classification"`
	Metrics        Metrics        `json:"metrics"`
}

// RunStressMap runs policy across a compact 3x3 grid (heterogeneity x
// workload, capacity fixed at the Capacity=1 near-boundary convention
// every Stage 14-16 scenario already used), one seed per cell -- 9
// quick virtual-time runs total, not a giant factorial matrix.
func RunStressMap(policyName string, seed int64) ([]StressMapCell, error) {
	spec, err := PolicyByName(policyName)
	if err != nil {
		return nil, err
	}
	cfg := backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 1.5 / 3.0} // 3-target topologies here, matching Stage 14/16's own fair-share-scaled convention

	var cells []StressMapCell
	cellSeed := seed
	for _, het := range heterogeneityTopologies() {
		for _, wl := range stressMapWorkloads() {
			seeds := replay.DeriveSeeds(cellSeed)
			cellSeed++
			arrivals, err := traffic.Generate(wl.p, wl.params, seeds.Traffic)
			if err != nil {
				return nil, fmt.Errorf("report: stress map %s/%s: generating traffic: %w", het.name, wl.name, err)
			}
			scenario := replay.Scenario{Targets: het.targets, Arrivals: arrivals, Horizon: clock.VirtualTime(CanonicalHorizon.Nanoseconds()), Seeds: seeds}
			v := engine.NewVirtualEngine()
			exp := engine.Experiment{ID: fmt.Sprintf("stress-map-%s-%s", het.name, wl.name), Scenario: scenario, Policy: spec}
			result, err := v.Run(exp)
			if err != nil {
				return nil, fmt.Errorf("report: stress map %s/%s: %w", het.name, wl.name, err)
			}
			m := AnalyzeTarget(result.WorldResult, het.targets, CanonicalCapacity, float64(CanonicalHorizon.Milliseconds()), cfg)
			class, _ := Classify(m)
			cells = append(cells, StressMapCell{Heterogeneity: het.name, Workload: wl.name, Classification: class, Metrics: m})
		}
	}
	return cells, nil
}

// classificationGlyph is the single-character legend RenderStressMap
// uses per cell -- kept to plain ASCII/simple symbols the data can
// actually support, not decorative graphics.
func classificationGlyph(c Classification) string {
	switch c {
	case Stable:
		return "OK"
	case RecoveryLimited:
		return "~ "
	case AcuteCollapse:
		return "AC"
	case ChronicCollapse:
		return "CH"
	default:
		return "? "
	}
}

// RenderStressMap renders cells as a heterogeneity x workload grid
// using the standard library's text/tabwriter, plus a legend -- no new
// rendering dependency, and no table-alignment code to hand-roll.
func RenderStressMap(policyName string, cells []StressMapCell) string {
	var b strings.Builder
	fmt.Fprintf(&b, "POLICY STRESS MAP: %s\n\n", policyName)
	fmt.Fprintln(&b, "EXPLORATORY ANALYSIS -- this grid is a new, small (9-cell) run. It is not a")
	fmt.Fprintln(&b, "reproduction of any specific Stage 13-16 experiment, and its numbers should")
	fmt.Fprintln(&b, "not be cited as a Stage 13-16 finding. See docs/StageArtifacts/Stage17-DiagnosticTooling.md.")
	fmt.Fprintln(&b)

	byCell := map[string]StressMapCell{}
	var workloads, heterogeneities []string
	seenW, seenH := map[string]bool{}, map[string]bool{}
	for _, c := range cells {
		byCell[c.Heterogeneity+"|"+c.Workload] = c
		if !seenW[c.Workload] {
			seenW[c.Workload] = true
			workloads = append(workloads, c.Workload)
		}
		if !seenH[c.Heterogeneity] {
			seenH[c.Heterogeneity] = true
			heterogeneities = append(heterogeneities, c.Heterogeneity)
		}
	}

	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	fmt.Fprint(tw, "heterogeneity")
	for _, w := range workloads {
		fmt.Fprintf(tw, "\t%s", w)
	}
	fmt.Fprintln(tw)
	for _, h := range heterogeneities {
		fmt.Fprint(tw, h)
		for _, w := range workloads {
			cell, ok := byCell[h+"|"+w]
			if !ok {
				fmt.Fprint(tw, "\t--")
				continue
			}
			fmt.Fprintf(tw, "\t%s", classificationGlyph(cell.Classification))
		}
		fmt.Fprintln(tw)
	}
	if err := tw.Flush(); err != nil {
		log.Printf("report: flushing stress map tabwriter: %v", err)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Legend: OK=STABLE  ~=RECOVERY_LIMITED  AC=ACUTE_COLLAPSE  CH=CHRONIC_COLLAPSE")
	return b.String()
}
