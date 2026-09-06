// Metamorphic invariant tests -- TRD §16's two named invariants ("2x
// delay -> latency must not decrease," "halved load -> utilization must
// monotonically decrease"), missing per the Stage 8 audit's F-07. Unlike
// this package's other adversarial cases (built from a golden scenario's
// specific structural properties), a metamorphic test doesn't assert a
// scenario's actual output against a hand-computed answer -- it asserts
// a RELATIONSHIP between two runs derived from each other by one
// controlled transformation, which is a genuinely different (and
// genuinely useful) kind of check when there's no independently-known
// "right answer" for either run individually.
package challenge

import (
	"testing"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/clock"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

func metamorphicTargets() []replay.TargetProfile {
	return []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 20 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 40 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
}

// meanLatencyMs returns the mean latency, in milliseconds, across every
// completion in result -- a local helper rather than a dependency on
// internal/tuning's pooled-metrics machinery, which this package
// deliberately avoids depending on (challenge exists to test routing/
// health/cache/network/replay/tuning FROM THE OUTSIDE, not to import the
// packages it might need to test).
func meanLatencyMs(t *testing.T, result replay.WorldResult) float64 {
	t.Helper()
	if len(result.Completions) == 0 {
		t.Fatal("expected at least one completion")
	}
	var total float64
	for _, c := range result.Completions {
		total += float64(c.Latency.Microseconds()) / 1000.0
	}
	return total / float64(len(result.Completions))
}

// TestMetamorphic_DoubledServiceTime_RoundRobin_LatencyMustNotDecrease
// is the primary form of TRD §16's first invariant: doubling every
// target's ServiceTime, with arrivals and Horizon held byte-for-byte
// identical between the two runs, must not produce a LOWER mean
// latency. Round Robin is the primary policy for this check because its
// routing decisions never consult ServiceTime at all -- the traffic MIX
// across targets is therefore identical between the baseline and
// doubled runs, making the outcome an exact, not just directional,
// prediction: mean latency must double (within floating-point
// tolerance), since every completion's latency is deterministically
// exactly its target's ServiceTime in this queueless virtual engine
// (see internal/replay/world.go's own doc comment on why mean latency
// equals the share-weighted average service time here).
func TestMetamorphic_DoubledServiceTime_RoundRobin_LatencyMustNotDecrease(t *testing.T) {
	targets := metamorphicTargets()
	maxDoubledSvc := 2 * 60 * time.Millisecond // the largest ServiceTime AFTER doubling -- horizon must clear this for both runs

	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 200, Horizon: 2 * time.Second, BaseRate: 100,
	}, 1)
	if err != nil {
		t.Fatalf("traffic.Generate failed: %v", err)
	}
	// Horizon computed generously from the DOUBLED (larger) max service
	// time and shared, unmodified, by both scenarios -- if the two runs
	// used different horizons (e.g. each computed AFTER its own
	// doubling), Stage 9's InFlightAtHorizon accounting fix (F-29) would
	// make "did latency change" partly a function of "were more
	// requests silently left in-flight at cutoff," an unrelated confound
	// this test must not let in.
	horizon := clock.VirtualTime((2*time.Second + maxDoubledSvc + 200*time.Millisecond).Nanoseconds())

	baseline := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: horizon}
	doubled := replay.Scenario{Targets: doubleServiceTimes(targets), Arrivals: arrivals, Horizon: horizon}

	baseResult, err := replay.RunWorld(baseline, replay.RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(baseline) failed: %v", err)
	}
	doubledResult, err := replay.RunWorld(doubled, replay.RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(doubled) failed: %v", err)
	}
	if baseResult.InFlightAtHorizon != 0 || doubledResult.InFlightAtHorizon != 0 {
		t.Fatalf("expected zero in-flight-at-horizon truncation in either run (would confound the comparison), got baseline=%d doubled=%d",
			baseResult.InFlightAtHorizon, doubledResult.InFlightAtHorizon)
	}

	baseMean := meanLatencyMs(t, baseResult)
	doubledMean := meanLatencyMs(t, doubledResult)

	if doubledMean < baseMean {
		t.Fatalf("doubling every target's ServiceTime decreased mean latency (%.3fms -> %.3fms) under Round Robin -- the invariant this test exists to catch", baseMean, doubledMean)
	}
	// Bonus tightness check, specific to Round Robin's identical traffic
	// mix: mean latency should have almost exactly doubled, not just
	// "not decreased."
	ratio := doubledMean / baseMean
	if ratio < 1.95 || ratio > 2.05 {
		t.Errorf("expected mean latency to almost exactly double under Round Robin's unchanged traffic mix, got ratio %.3f (base=%.3fms, doubled=%.3fms)", ratio, baseMean, doubledMean)
	}
}

// TestMetamorphic_DoubledServiceTime_Adaptive_WeakerCheck is the
// secondary, explicitly weaker form of the same invariant under
// AdaptivePolicy(). Unlike Round Robin, Adaptive's OWN routing decisions
// depend on ServiceTime (via its Latency signal and staleness
// mechanism), so doubling every target's ServiceTime can change WHICH
// target each arrival is routed to, not just how long that routing
// takes -- meaning an exact "latency must double" prediction does not
// apply, and even "latency must not decrease at all" is not a
// mathematical certainty (see this file's own package-level discussion
// in docs/StageArtifacts/Stage10-Plan.md §10.4: a per-target invariant
// holds exactly, but a POOLED mean does not, once the routing mix
// itself is allowed to shift). This test therefore asserts only a
// generous, soft bound (doubled mean must not fall below 90% of
// baseline) and reports the actual numbers as a finding either way --
// a large violation would be a real, interesting result worth
// investigating, not evidence of a test bug.
func TestMetamorphic_DoubledServiceTime_Adaptive_WeakerCheck(t *testing.T) {
	targets := metamorphicTargets()
	maxDoubledSvc := 2 * 60 * time.Millisecond

	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 200, Horizon: 2 * time.Second, BaseRate: 100,
		KeyFunc: traffic.HotColdKeys(0.5),
	}, 1)
	if err != nil {
		t.Fatalf("traffic.Generate failed: %v", err)
	}
	horizon := clock.VirtualTime((2*time.Second + maxDoubledSvc + 200*time.Millisecond).Nanoseconds())

	baseline := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: horizon}
	doubled := replay.Scenario{Targets: doubleServiceTimes(targets), Arrivals: arrivals, Horizon: horizon}

	baseResult, err := replay.RunWorld(baseline, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("RunWorld(baseline, adaptive) failed: %v", err)
	}
	doubledResult, err := replay.RunWorld(doubled, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("RunWorld(doubled, adaptive) failed: %v", err)
	}

	baseMean := meanLatencyMs(t, baseResult)
	doubledMean := meanLatencyMs(t, doubledResult)
	ratio := doubledMean / baseMean

	t.Logf("Finding: doubling ServiceTime under Adaptive routing changed mean latency %.3fms -> %.3fms (ratio %.3f); Round Robin's identical traffic mix gives an exact 2x prediction, Adaptive's own re-routing in response to the change does not.",
		baseMean, doubledMean, ratio)

	const minAcceptableRatio = 0.9
	if ratio < minAcceptableRatio {
		t.Errorf("doubling every target's ServiceTime decreased Adaptive's mean latency by more than the generous %.0f%% tolerance (ratio %.3f, base=%.3fms, doubled=%.3fms) -- investigate whether this is a real re-routing effect or a bug",
			(1-minAcceptableRatio)*100, ratio, baseMean, doubledMean)
	}
}

func doubleServiceTimes(targets []replay.TargetProfile) []replay.TargetProfile {
	out := make([]replay.TargetProfile, len(targets))
	for i, t := range targets {
		out[i] = replay.TargetProfile{Name: t.Name, ServiceTime: t.ServiceTime * 2}
	}
	return out
}

// TestMetamorphic_HalvedArrivalCount_UtilizationMustNotIncrease is TRD
// §16's second invariant: halving the arrival COUNT within the SAME
// fixed Horizon (not doubling the Horizon for the same count, which
// would just relabel the measurement window rather than actually
// reduce offered load) must not increase any target's utilization.
// Round Robin is used here for the same reason as the primary latency
// check: its traffic mix doesn't depend on ServiceTime, so halving
// arrival count halves each target's completed count roughly evenly
// (up to a +/-1 rounding difference from the cyclic assignment), a
// robust, non-knife-edge margin for a "<=" check.
func TestMetamorphic_HalvedArrivalCount_UtilizationMustNotIncrease(t *testing.T) {
	targets := metamorphicTargets()
	const horizonDuration = 3 * time.Second
	horizon := clock.VirtualTime(horizonDuration.Nanoseconds())

	baseArrivals, err := traffic.Generate(traffic.Constant, traffic.Params{Requests: 300, Horizon: horizonDuration, BaseRate: 100}, 1)
	if err != nil {
		t.Fatalf("traffic.Generate(baseline) failed: %v", err)
	}
	halvedArrivals, err := traffic.Generate(traffic.Constant, traffic.Params{Requests: 150, Horizon: horizonDuration, BaseRate: 100}, 1)
	if err != nil {
		t.Fatalf("traffic.Generate(halved) failed: %v", err)
	}

	baseline := replay.Scenario{Targets: targets, Arrivals: baseArrivals, Horizon: horizon}
	halved := replay.Scenario{Targets: targets, Arrivals: halvedArrivals, Horizon: horizon}

	baseResult, err := replay.RunWorld(baseline, replay.RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(baseline) failed: %v", err)
	}
	halvedResult, err := replay.RunWorld(halved, replay.RoundRobinPolicy())
	if err != nil {
		t.Fatalf("RunWorld(halved) failed: %v", err)
	}

	baseUtil, err := attribution.UtilizationFromWorld(baseResult, targets, horizonDuration)
	if err != nil {
		t.Fatalf("UtilizationFromWorld(baseline) failed: %v", err)
	}
	halvedUtil, err := attribution.UtilizationFromWorld(halvedResult, targets, horizonDuration)
	if err != nil {
		t.Fatalf("UtilizationFromWorld(halved) failed: %v", err)
	}

	for _, target := range targets {
		if halvedUtil[target.Name] > baseUtil[target.Name] {
			t.Errorf("target %s: halved-arrival-count utilization %.4f exceeds baseline %.4f -- expected it to not increase",
				target.Name, halvedUtil[target.Name], baseUtil[target.Name])
		}
		t.Logf("target %s: baseline rho=%.4f, halved rho=%.4f", target.Name, baseUtil[target.Name], halvedUtil[target.Name])
	}
}
