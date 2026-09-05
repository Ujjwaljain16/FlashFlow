package tuning

import (
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
)

// Metrics are the raw, generically-computable quantities this
// objective is built from -- generic because they must be computable
// for ANY scenario this package's generator can produce, not just ones
// with known phase boundaries. Adaptation delay and oscillation (master
// context rule 6) are NOT included here: both require a scenario to
// declare when its "best" target changes (007-C/007-D's hand-built
// phase structure), which DefaultScenarioSpace's randomly-generated
// scenarios don't carry. Those remain a golden-scenario/challenge-suite
// concern (008-E), not a property this general-purpose tuning
// objective can honestly claim to measure.
type Metrics struct {
	MeanLatencyMs float64 // share-weighted average completion latency -- see ComputeScores' doc comment for why this, not a percentile, drives the objective
	P50LatencyMs  float64
	P99LatencyMs  float64
	RejectedRate  float64 // rejected / (rejected + completed), pooled across the scenario set
	MeanMaxShare  float64 // mean, across scenarios, of the largest single target's share of that scenario's completions
}

// ComputeMetrics pools completions across every result in results (one
// per scenario in a set) into a single latency distribution -- a
// deliberate choice, not an oversight: this project's Development/
// Holdout sets mix scenario shapes (2-5 targets, with or without a
// failure), and a pooled p99 answers "how does this configuration
// perform across the kind of traffic this set represents," while an
// unweighted mean-of-per-scenario-p99s would let a handful of
// low-request scenarios distort the aggregate as much as the rest
// combined. MeanMaxShare is deliberately averaged per-scenario instead
// (a share is only meaningful relative to its own scenario's target
// count and traffic).
func ComputeMetrics(results []replay.WorldResult) (Metrics, error) {
	var latenciesMs []float64
	totalRejected, totalCompleted := 0, 0
	var maxShares []float64

	for _, r := range results {
		totalRejected += r.RejectedCount
		totalCompleted += len(r.Completions)
		for _, c := range r.Completions {
			latenciesMs = append(latenciesMs, float64(c.Latency.Microseconds())/1000.0)
		}
		if len(r.CompletedByTarget) > 0 {
			max := 0
			total := 0
			for _, n := range r.CompletedByTarget {
				if n > max {
					max = n
				}
				total += n
			}
			if total > 0 {
				maxShares = append(maxShares, float64(max)/float64(total))
			}
		}
	}

	if len(latenciesMs) == 0 {
		return Metrics{}, ErrNoCompletions
	}

	meanLatency, err := statistics.Mean(latenciesMs)
	if err != nil {
		return Metrics{}, err
	}
	p50, err := statistics.Percentile(latenciesMs, 50)
	if err != nil {
		return Metrics{}, err
	}
	p99, err := statistics.Percentile(latenciesMs, 99)
	if err != nil {
		return Metrics{}, err
	}
	meanMaxShare, err := statistics.Mean(maxShares)
	if err != nil {
		return Metrics{}, err
	}

	total := totalRejected + totalCompleted
	rejectedRate := 0.0
	if total > 0 {
		rejectedRate = float64(totalRejected) / float64(total)
	}

	return Metrics{
		MeanLatencyMs: meanLatency, P50LatencyMs: p50, P99LatencyMs: p99,
		RejectedRate: rejectedRate, MeanMaxShare: meanMaxShare,
	}, nil
}

// ErrNoCompletions is returned when a scenario set produced zero
// completions at all -- every request rejected, or an empty set. A
// tuning candidate that hits this should be treated as a scored-worst
// evaluation, not silently skipped (see search.go).
var ErrNoCompletions = errNoCompletions{}

type errNoCompletions struct{}

func (errNoCompletions) Error() string { return "tuning: scenario set produced no completions" }

// Scores are Metrics transformed to a common [0,1]-ish scale where
// higher always means better -- the same convention TargetScore already
// uses in internal/proxy/adaptive.go, kept consistent here so the
// tuner's objective isn't a second, differently-oriented scoring
// language layered on top of the router's own.
type Scores struct {
	LatencyScore  float64 // 1 - meanLatency/(meanLatency+RefLatencyMs); reuses AdaptiveSelector.scoreLatency's own bounded ratio transform
	RejectScore   float64 // 1 - RejectedRate (already a proportion in [0,1])
	FairnessScore float64 // 1 - MeanMaxShare (perfectly even split -> close to 1; total concentration -> 0)
}

// RefLatencyMs is the latency (in milliseconds) at which LatencyScore is
// "50% penalized" -- reusing the exact value and rationale of
// proxy.DefaultAdaptiveConfig().ReferenceLatency (100ms) rather than
// inventing a second, differently-justified constant for the same kind
// of transform.
const RefLatencyMs = 100.0

// ComputeScores converts m into the bounded, higher-is-better Scores
// ComputeUtility and ParetoFrontier both operate on.
//
// LatencyScore is built from MeanLatencyMs, not a percentile -- a
// deliberate fix discovered while validating 008-F's final policy
// comparison, not the original design. An earlier version used
// P99LatencyMs, on the reasoning that Stage 6 had made tail latency
// this project's usual latency concern. On this project's randomly
// generated scenarios (300 requests each), that made LatencyScore
// nearly USELESS as a discriminator: p99 sits at roughly the 3rd-worst
// sample of 300, so as long as ANY policy sends even a handful of
// requests to a scenario's single worst target -- true of literally
// every policy in this project, including Adaptive, via cold-start
// exploration alone -- p99 lands on that target's raw service time
// regardless of how much OTHER traffic that policy correctly steered
// away from it. Direct evidence: in one generated scenario, Adaptive's
// median latency was 52ms against Round Robin's 130ms (a real, large,
// mechanistically obvious routing-quality difference), while both
// policies' p99 was IDENTICAL at 166.5ms. With p99 contributing almost
// no signal, the 0.1-weighted FairnessScore ended up dominating the
// ranking instead -- and fairness (an even split) is not what a
// heterogeneity-aware router should be judged on, since successfully
// avoiding a bad target necessarily makes the split LESS even, and
// that manifested as Round Robin appearing to "beat" every adaptive
// policy on 90% of Development scenarios, an artifact of the metric,
// not a real finding. Since this replay engine has no queueing model
// (a request's latency is exactly its dispatched target's fixed
// service time, no contention-based extra delay), mean latency across
// a scenario's completions is mathematically exactly the
// share-weighted average of each target's service time -- precisely
// the quantity a routing policy's target-selection quality should be
// judged on, with none of a percentile's small-sample discreteness
// blind spot. P99LatencyMs/P50LatencyMs remain in Metrics as
// informational tail-latency data (matching Stage 6's own interest in
// it), just no longer the quantity the objective optimizes.
func ComputeScores(m Metrics) Scores {
	return Scores{
		LatencyScore:  1 - m.MeanLatencyMs/(m.MeanLatencyMs+RefLatencyMs),
		RejectScore:   1 - m.RejectedRate,
		FairnessScore: 1 - m.MeanMaxShare,
	}
}

// ObjectiveWeights combine Scores into one scalar utility for Random
// Search to rank candidates by. These are an explicit evaluation
// preference (master context rule 8), not a claim of scientific
// optimality: Latency is weighted highest because it is this project's
// most consistently-reported user-facing metric since Stage 1;
// RejectScore next because a configuration that routes to unhealthy or
// overloaded targets, causing rejections, is worse than one that is
// merely a bit slower; Fairness lowest because 007-B/007-H already
// established Adaptive's fairness advantage is real but secondary to
// latency/availability for this project's own research questions.
type ObjectiveWeights struct {
	Latency  float64
	Reject   float64
	Fairness float64
}

// DefaultObjectiveWeights sums to 1, purely for readability -- Utility
// doesn't require that, for the same scale-invariance reason
// AdaptiveWeights doesn't (see space.go): only relative ratios matter
// when the only use of the resulting number is ranking candidates
// against each other.
func DefaultObjectiveWeights() ObjectiveWeights {
	return ObjectiveWeights{Latency: 0.6, Reject: 0.3, Fairness: 0.1}
}

// Utility combines s into one ranking scalar via w. Higher is better,
// matching TargetScore.CombinedScore's own convention.
func Utility(s Scores, w ObjectiveWeights) float64 {
	return w.Latency*s.LatencyScore + w.Reject*s.RejectScore + w.Fairness*s.FairnessScore
}

// Dominates reports whether a is at least as good as b on every score
// dimension and strictly better on at least one -- the standard Pareto
// dominance relation. Used to answer "is there evidence one candidate
// is simply better than another" without collapsing multiple objectives
// into a single number, per master context rule 7.
func (a Scores) Dominates(b Scores) bool {
	atLeastAsGood := a.LatencyScore >= b.LatencyScore && a.RejectScore >= b.RejectScore && a.FairnessScore >= b.FairnessScore
	strictlyBetter := a.LatencyScore > b.LatencyScore || a.RejectScore > b.RejectScore || a.FairnessScore > b.FairnessScore
	return atLeastAsGood && strictlyBetter
}

// ParetoFrontier returns the indices into scores of every
// non-dominated candidate: configurations for which no other candidate
// in the set is at least as good on every dimension and strictly better
// on one. This is the honest multi-objective answer master context
// rule 7 asks for -- "A and B are both Pareto-efficient" is a legitimate
// conclusion, not a failure to pick a winner.
func ParetoFrontier(scores []Scores) []int {
	var frontier []int
	for i, s := range scores {
		dominated := false
		for j, other := range scores {
			if i == j {
				continue
			}
			if other.Dominates(s) {
				dominated = true
				break
			}
		}
		if !dominated {
			frontier = append(frontier, i)
		}
	}
	return frontier
}
