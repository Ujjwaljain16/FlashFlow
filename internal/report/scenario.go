package report

import (
	"fmt"
	"log"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

// CanonicalTargetCount, CanonicalCapacity, and CanonicalHorizon are the
// exact values experiment-015a and experiment-016-flagship already
// established and validated across multiple seeds -- reused here
// verbatim (not reinvented) so this package's own output is directly
// comparable to Stage 15/16's own published numbers.
const (
	CanonicalTargetCount = 5
	CanonicalCapacity    = 1
	CanonicalHorizon     = 8 * time.Second
	// CanonicalBaseSeed is the first seed RunCanonicalScenario uses --
	// exported so a Reproduce view can state the exact seed(s) a report
	// used without guessing at this package's own internal convention.
	CanonicalBaseSeed int64 = 17000
)

// CanonicalTargets returns the same 5 heterogeneous, Capacity=1 targets
// (15/30/45/60/75ms) used throughout Stage 15/16 -- extracted here once
// so this package's own CLI doesn't duplicate it a further time; the
// existing experiment binaries that already define this inline are left
// untouched, since they are historical artifacts, not living code that
// should be refactored after the fact.
func CanonicalTargets() []replay.TargetProfile {
	out := make([]replay.TargetProfile, CanonicalTargetCount)
	for i := 0; i < CanonicalTargetCount; i++ {
		out[i] = replay.TargetProfile{
			Name:        fmt.Sprintf("edge-%02d", i),
			ServiceTime: time.Duration(15*(i+1)) * time.Millisecond,
			Capacity:    CanonicalCapacity,
		}
	}
	return out
}

// CanonicalArrivals returns the same FlashCrowd workload (BaseRate=20,
// PeakRate=300, peak at t=2.5s) experiment-015a/016-flagship already
// use, for the given seed and jitter fraction.
func CanonicalArrivals(seed int64, jitterFraction float64) (arrivals []replay.Arrival, seeds replay.SeedTree) {
	seeds = replay.DeriveSeeds(seed)
	var err error
	arrivals, err = traffic.Generate(traffic.FlashCrowd, traffic.Params{
		Requests: 600, Horizon: CanonicalHorizon, BaseRate: 20, PeakRate: 300,
		BurstAt: 2500 * time.Millisecond, BurstWidth: 1000 * time.Millisecond,
		KeyFunc: traffic.HotColdKeys(0.5), JitterFraction: jitterFraction,
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("report: generating canonical traffic: %v", err)
	}
	return arrivals, seeds
}

// CanonicalScenarioLabel is the human-readable scenario description
// used in every rendered report, matching Stage 16's own flagship
// description exactly.
const CanonicalScenarioLabel = "5 targets (15-75ms) / Capacity=1 / FlashCrowd (peak at t=2.5s) / 8s horizon"

// CanonicalCongestionConfig is the same convention every Stage 14-16
// canonical-scenario experiment already used (ratio threshold 1.0, a
// 20-decision trailing window, share threshold scaled to 1.5x the
// topology's own fair share -- see cmd/experiment-016-flagship's own
// comment for why this generalizes, not replaces, the fixed 0.5 Stage
// 14/15 used at N=3). Fixed to CanonicalTargetCount since every
// canonical-scenario caller uses the same 5-target topology.
func CanonicalCongestionConfig() backlog.CongestionConfig {
	return backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 1.5 / float64(CanonicalTargetCount)}
}

// RunCanonicalScenario runs every policy in PolicyNames across
// seedCount independent seeds of the canonical scenario and returns the
// resulting ScenarioReport -- the shared implementation behind both
// `cmd/flashflow report` and the dashboard's own Control Room view, so
// neither duplicates the other.
func RunCanonicalScenario(seedCount int) (ScenarioReport, error) {
	targets := CanonicalTargets()
	capacity := CanonicalCapacity
	horizonMs := float64(CanonicalHorizon.Milliseconds())
	cfg := CanonicalCongestionConfig()

	perPolicy := map[string][]*replay.WorldResult{}
	usedSeeds := make([]int64, 0, seedCount)
	for i := 0; i < seedCount; i++ {
		seed := CanonicalBaseSeed + int64(i)
		usedSeeds = append(usedSeeds, seed)
		arrivals, seeds := CanonicalArrivals(seed, 0.3)
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(CanonicalHorizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		for j, name := range PolicyNames() {
			spec, err := PolicyByName(name)
			if err != nil {
				return ScenarioReport{}, err
			}
			exp := engine.Experiment{ID: fmt.Sprintf("canonical-seed%d-%s", seed, name), Scenario: scenario, Policy: spec}
			var result engine.RunResult
			var err2 error
			if j == 0 {
				result, err2 = v.Run(exp)
			} else {
				result, err2 = v.Replay(exp, spec)
			}
			if err2 != nil {
				return ScenarioReport{}, fmt.Errorf("report: seed %d/%s: %w", seed, name, err2)
			}
			perPolicy[name] = append(perPolicy[name], result.WorldResult)
		}
	}
	return BuildScenarioReport(CanonicalScenarioLabel, targets, capacity, horizonMs, cfg, perPolicy, PolicyNames(), usedSeeds), nil
}
