package report

import (
	"fmt"
	"log"
	"time"

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
