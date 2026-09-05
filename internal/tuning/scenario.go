package tuning

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

// ScenarioSpace bounds the dimensions this project's scenario generator
// actually draws from. It is deliberately narrower than the master
// context's full illustrative list (targets, capacity, latency,
// arrival rate, burstiness, failure/recovery timing, cache popularity,
// TTL, network impairment, health cadence): internal/replay.Scenario
// has no cache/TTL model and no netsim wiring at all (Adaptive's Cache
// signal is router-maintained key affinity, not a real TTL cache; Stage
// 7 never wired internal/netsim into RunWorld). Generating scenario
// dimensions the engine can't actually express would violate the
// master context's own "an optimizer should never be allowed to create
// an invalid experiment" rule in the worst way -- silently ignored
// rather than rejected. What's generated here (target count, per-target
// service time heterogeneity, arrival spacing/jitter, an optional
// failure/recovery window) is exactly what RunWorld can execute today.
type ScenarioSpace struct {
	MinTargets, MaxTargets                 int
	MinServiceTime, MaxServiceTime         time.Duration
	ArrivalSpacing                         time.Duration
	JitterFraction                         float64 // arrival jitter as a fraction of ArrivalSpacing, e.g. 0.4 = +/-40%
	Requests                               int
	FailureProbability                     float64 // chance a generated scenario includes one failure/recovery window
	MinFailureDuration, MaxFailureDuration time.Duration
}

// DefaultScenarioSpace mirrors the scale of every hand-built Stage 7
// scenario (007-B's 3 targets/300 requests/5ms spacing, 007-D/G/H's
// 20-200ms service times, 007-E/G's ~1.1s failure windows): 2-5
// targets, 5-200ms service times, a 5ms nominal arrival grid with up to
// +/-40% jitter (007-H's precedent, widened since this generator must
// cover many scenario shapes, not perturb one specific one), 300
// requests, and a 50% chance of one failure/recovery window lasting
// 300ms-1.5s.
func DefaultScenarioSpace() ScenarioSpace {
	return ScenarioSpace{
		MinTargets: 2, MaxTargets: 5,
		MinServiceTime: 5 * time.Millisecond, MaxServiceTime: 200 * time.Millisecond,
		ArrivalSpacing: 5 * time.Millisecond, JitterFraction: 0.4,
		Requests:           300,
		FailureProbability: 0.5,
		MinFailureDuration: 300 * time.Millisecond,
		MaxFailureDuration: 1500 * time.Millisecond,
	}
}

var targetNames = []string{"edge-a", "edge-b", "edge-c", "edge-d", "edge-e"}

func keyForIndex(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	return fmt.Sprintf("/cold-%d", i%3)
}

// Generate builds one valid, reproducible Scenario from seed: every
// random draw comes from a *rand.Rand constructed fresh from seed, so
// the same seed always produces the byte-for-byte identical Scenario --
// the seed IS the scenario's identity, recorded alongside every
// evaluation in the search ledger.
func (ss ScenarioSpace) Generate(seed int64) replay.Scenario {
	rng := rand.New(rand.NewSource(seed))

	n := ss.MinTargets + rng.Intn(ss.MaxTargets-ss.MinTargets+1)
	targets := make([]replay.TargetProfile, n)
	svcRange := int64(ss.MaxServiceTime - ss.MinServiceTime)
	for i := 0; i < n; i++ {
		svc := ss.MinServiceTime + time.Duration(rng.Int63n(svcRange+1))
		targets[i] = replay.TargetProfile{Name: targetNames[i], ServiceTime: svc}
	}

	arrivals := make([]replay.Arrival, ss.Requests)
	jitterRange := float64(ss.ArrivalSpacing) * ss.JitterFraction
	for i := 0; i < ss.Requests; i++ {
		nominal := ss.ArrivalSpacing.Nanoseconds() * int64(i)
		jitter := (rng.Float64()*2 - 1) * jitterRange
		at := nominal + int64(jitter)
		if at < 0 {
			at = 0
		}
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(at), Key: keyForIndex(i)}
	}
	lastArrival := clock.VirtualTime(ss.ArrivalSpacing.Nanoseconds() * int64(ss.Requests-1))

	scenario := replay.Scenario{
		Targets:  targets,
		Arrivals: arrivals,
		Seed:     seed,
	}

	var failEnd clock.VirtualTime
	if rng.Float64() < ss.FailureProbability {
		failTarget := targets[rng.Intn(n)].Name
		durRange := int64(ss.MaxFailureDuration - ss.MinFailureDuration)
		duration := ss.MinFailureDuration + time.Duration(rng.Int63n(durRange+1))
		// DownAt sampled within the first 60% of the arrival span, so
		// there's always meaningful traffic both before and after the
		// failure -- a failure starting in the last request or two would
		// never actually get exercised by any routing decision.
		var downAt clock.VirtualTime
		if window := int64(float64(lastArrival) * 0.6); window > 0 {
			downAt = clock.VirtualTime(rng.Int63n(window))
		}
		upAt := downAt.Add(duration)
		scenario.Failures = []replay.FailureWindow{{Target: failTarget, DownAt: downAt, UpAt: upAt}}
		scenario.UseHealthRegistry = true
		failEnd = upAt
	}

	// Horizon must clear the last arrival's own completion (using the
	// slowest possible target's service time as the bound) and the
	// failure's UpAt if later -- RunWorld's probe Ticker never stops
	// itself, so an insufficient Horizon would either truncate real
	// traffic or leave the ticker running forever under RunUntilEmpty.
	horizon := lastArrival.Add(ss.MaxServiceTime + 200*time.Millisecond)
	if failEnd > 0 {
		end := failEnd.Add(200 * time.Millisecond)
		if end > horizon {
			horizon = end
		}
	}
	scenario.Horizon = horizon

	return scenario
}

// GenerateSet builds count Scenarios from consecutive seeds starting at
// startSeed. Development and Holdout sets must use disjoint seed ranges
// (see space.go's Hash for the analogous provenance discipline on the
// config side) -- enforced by convention here (see NewSplit) rather than
// by any runtime check, since a seed range is a property of how a set
// was generated, not of any individual Scenario.
func (ss ScenarioSpace) GenerateSet(startSeed int64, count int) []replay.Scenario {
	scenarios := make([]replay.Scenario, count)
	for i := 0; i < count; i++ {
		scenarios[i] = ss.Generate(startSeed + int64(i))
	}
	return scenarios
}

// Split is the sacred Development/Holdout partition (rule 9): disjoint
// seed ranges, generated once and never regenerated except through an
// explicit, recorded decision (see the README's contamination-handling
// note if that ever becomes necessary). Challenge scenarios are
// deliberately NOT part of this generator -- they are hand-crafted
// adversarial cases (008-E), not randomly sampled ones, matching the
// master context's own description of the challenge suite as scenarios
// built specifically to break the system, not drawn from the same
// distribution as ordinary traffic.
type Split struct {
	Development []replay.Scenario
	Holdout     []replay.Scenario
}

// DevelopmentSeedStart/HoldoutSeedStart fix the two seed ranges this
// project's Development and Holdout sets are generated from. Holdout
// starts at a seed far enough past Development's range (100,000 vs 40
// scenarios starting at 1) that no accidental off-by-one or range
// miscalculation could ever cause overlap.
const (
	DevelopmentSeedStart int64 = 1
	HoldoutSeedStart     int64 = 100_001
)

// NewSplit generates the standard Development (40 scenarios) / Holdout
// (20 scenarios) split from ss. 40 development scenarios gives Random
// Search a reasonably diverse training distribution without making each
// candidate evaluation prohibitively expensive (40 RunWorld calls per
// candidate); 20 holdout scenarios is enough to compute a meaningful
// paired statistic (see 007-H's own use of 30 replicates for a
// comparable claim) without inflating the "look once" cost of the
// holdout step, which by design must never be re-run to chase a better
// number.
func NewSplit(ss ScenarioSpace) Split {
	return Split{
		Development: ss.GenerateSet(DevelopmentSeedStart, 40),
		Holdout:     ss.GenerateSet(HoldoutSeedStart, 20),
	}
}

// ScenarioSetHash returns a short, stable identifier for an ordered list
// of Scenarios, for search-ledger provenance and evaluation-result
// cache keys (see cache.go) -- the scenario-set half of the "config +
// scenario + seed -> cached result" key the master context describes.
// Since every Scenario here is fully determined by its own Seed (via
// ScenarioSpace.Generate), hashing the ordered sequence of Seeds is
// sufficient to identify "this exact set" without re-serializing every
// arrival and target profile.
func ScenarioSetHash(scenarios []replay.Scenario) string {
	var sb strings.Builder
	for _, s := range scenarios {
		fmt.Fprintf(&sb, "%d,", s.Seed)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])[:16]
}
