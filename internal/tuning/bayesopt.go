package tuning

import (
	"math"
	"math/rand"

	"flashflow/internal/proxy"
)

// gpModel is a fitted Gaussian Process: the Cholesky factor of the
// training kernel matrix (plus a small noise/jitter term for numerical
// stability) and the precomputed K^-1*y solve, from which both the
// posterior mean and variance at any new point can be read off without
// ever forming K^-1 explicitly.
type gpModel struct {
	x           [][]float64
	alpha       []float64
	l           []float64
	n           int
	lengthScale float64
	signalVar   float64
}

// kernel is the squared-exponential (RBF) covariance function --
// Stage 10's confirmed choice: a fixed length-scale, not a learned one
// (learning kernel hyperparameters via marginal-likelihood
// optimization is a well-known but substantially harder problem this
// project's own evidence-driven scope discipline doesn't justify
// building for a 5-dimensional, ~200-evaluation search that Stage 8
// already showed Random Search handles without difficulty).
func kernel(x1, x2 []float64, lengthScale, signalVar float64) float64 {
	var sumSq float64
	for i := range x1 {
		d := x1[i] - x2[i]
		sumSq += d * d
	}
	return signalVar * math.Exp(-sumSq/(2*lengthScale*lengthScale))
}

// fitGP builds the GP's kernel matrix over x/y (adding noiseVar to the
// diagonal, both as a standard observation-noise term and as numerical
// jitter keeping the matrix safely positive-definite even when two
// training points are very close together), Cholesky-factors it, and
// precomputes alpha = K^-1*y via solveCholesky.
func fitGP(x [][]float64, y []float64, lengthScale, signalVar, noiseVar float64) (*gpModel, error) {
	n := len(x)
	k := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			v := kernel(x[i], x[j], lengthScale, signalVar)
			if i == j {
				v += noiseVar
			}
			k[i*n+j] = v
		}
	}
	l, err := choleskyLower(k, n)
	if err != nil {
		return nil, err
	}
	alpha := solveCholesky(l, n, y)
	return &gpModel{x: x, alpha: alpha, l: l, n: n, lengthScale: lengthScale, signalVar: signalVar}, nil
}

// predict returns the GP posterior mean and standard deviation at xNew.
// Variance is signalVar (= kernel(xNew,xNew), since sumSq=0) minus the
// "explained" variance k_star^T*K^-1*k_star, computed via one more
// forward-substitution rather than forming K^-1 explicitly -- the
// standard GP prediction formula.
func (m *gpModel) predict(xNew []float64) (mean, stddev float64) {
	kStar := make([]float64, m.n)
	for i := 0; i < m.n; i++ {
		kStar[i] = kernel(m.x[i], xNew, m.lengthScale, m.signalVar)
	}
	for i := range kStar {
		mean += kStar[i] * m.alpha[i]
	}

	v := forwardSubstitute(m.l, m.n, kStar)
	var explained float64
	for i := range v {
		explained += v[i] * v[i]
	}
	variance := m.signalVar - explained
	if variance < 0 {
		// A numerical-precision guard, not a modeling correction:
		// variance can't legitimately be negative, but floating-point
		// rounding in the Cholesky solve can push a near-zero true
		// variance slightly below zero.
		variance = 0
	}
	return mean, math.Sqrt(variance)
}

// expectedImprovement is the standard EI acquisition function for
// maximization: how much better than the best-observed value (best) a
// point with posterior mean/stddev is expected to be, in expectation
// over the GP's own uncertainty, with xi as a small exploration bonus
// (a purely exploitative EI with xi=0 tends to cluster suggestions
// right at the current best; a positive xi encourages trying points the
// model is less certain about).
func expectedImprovement(mean, stddev, best, xi float64) float64 {
	if stddev <= 0 {
		if mean > best {
			return mean - best
		}
		return 0
	}
	z := (mean - best - xi) / stddev
	return (mean-best-xi)*normalCDF(z) + stddev*normalPDF(z)
}

func normalCDF(z float64) float64 { return 0.5 * (1 + math.Erf(z/math.Sqrt2)) }
func normalPDF(z float64) float64 { return math.Exp(-z*z/2) / math.Sqrt(2*math.Pi) }

// BayesOptTuner is a hand-rolled Bayesian Optimization tuner: a
// squared-exponential-kernel GP fit to every valid prior trial, whose
// posterior feeds an Expected Improvement acquisition function
// maximized over a random candidate pool (not a gradient-based
// continuous optimizer -- with a few hundred candidates in 5
// dimensions, random search over the acquisition function itself is
// simple, robust, and fast enough that a more sophisticated inner
// optimizer was never earned).
//
// Framing to lead with in any report of this tuner's results (Stage 10
// plan's own instruction): Stage 8 already showed Random Search
// converges by evaluation 24 of 200 and plateaus for the rest, with no
// evidence the search space needs a better optimizer. This tuner was
// built to honor the PRD's tuner-progression promise, not because
// evidence demanded it -- report honestly if it doesn't outperform
// Random Search on this project's own scenarios, since that would be
// the expected outcome given Stage 8's own findings, not a defect in
// this implementation.
type BayesOptTuner struct {
	rng           *rand.Rand
	space         ConfigSpace
	seed          int64
	candidatePool int
	lengthScale   float64
	signalVar     float64
	noiseVar      float64
	xi            float64
}

// NewBayesOptTuner constructs a BayesOptTuner with fixed, documented
// hyperparameters (Stage 10's confirmed decision: a fixed length-scale,
// not a learned one). candidatePool=500 random candidates per Suggest
// call is generous relative to this search space's 5 dimensions and
// ~200-evaluation budget while staying computationally trivial (each
// candidate's EI evaluation is O(n) in the number of prior trials, so
// 500 candidates x 200 trials = 100,000 kernel evaluations at the
// worst/last iteration, still well under a second).
func NewBayesOptTuner(seed int64, space ConfigSpace) *BayesOptTuner {
	return &BayesOptTuner{
		rng: rand.New(rand.NewSource(seed)), space: space, seed: seed,
		candidatePool: 500,
		lengthScale:   0.3, // config coordinates are normalized to roughly [0,1]; 0.3 gives meaningful correlation across ~30% of the space's extent
		signalVar:     1.0,
		noiseVar:      1e-6, // pure numerical jitter -- this project's evaluations are deterministic given a fixed scenario set, not genuinely noisy
		xi:            0.01,
	}
}

func (t *BayesOptTuner) Space() ConfigSpace { return t.space }
func (t *BayesOptTuner) Seed() int64        { return t.seed }
func (t *BayesOptTuner) Name() string       { return "bayesopt-v1" }

// Suggest fits a GP to every valid prior trial and returns the
// candidate (from a random pool) maximizing Expected Improvement.
// Falls back to plain uniform sampling when there isn't yet enough
// data to fit a meaningful model (fewer than 2 valid prior trials --
// standard Bayesian Optimization practice seeds the model with a
// handful of random points before the acquisition function has
// anything to condition on) or when the GP fit itself fails
// numerically (e.g. near-duplicate points making the kernel matrix
// ill-conditioned) -- Stage 10's own framing applies here too: don't
// crash just because the math got unlucky, report what happened
// honestly instead.
func (t *BayesOptTuner) Suggest(previous []TrialResult) proxy.AdaptiveConfig {
	var x [][]float64
	var y []float64
	for _, p := range previous {
		if !p.Valid {
			continue
		}
		x = append(x, t.toVector(p.Config))
		y = append(y, p.Utility)
	}
	if len(x) < 2 {
		return t.space.Sample(t.rng)
	}

	gp, err := fitGP(x, y, t.lengthScale, t.signalVar, t.noiseVar)
	if err != nil {
		return t.space.Sample(t.rng)
	}

	best := y[0]
	for _, v := range y {
		if v > best {
			best = v
		}
	}

	bestEI := math.Inf(-1)
	bestCfg := t.space.Sample(t.rng) // a safe default if, somehow, no candidate ever beats -Inf (can't happen given EI>=0 always, but avoids ever returning a zero-value config)
	for i := 0; i < t.candidatePool; i++ {
		cand := t.space.Sample(t.rng)
		mean, stddev := gp.predict(t.toVector(cand))
		ei := expectedImprovement(mean, stddev, best, t.xi)
		if ei > bestEI {
			bestEI = ei
			bestCfg = cand
		}
	}
	return bestCfg
}

// toVector maps cfg to its 5 continuous coordinates for the GP's
// kernel: the 3 free simplex weights as-is, plus ReferenceLatency/
// StaleAfter normalized to [0,1] fractions of their ConfigSpace bounds
// -- putting every dimension on a comparable [0,1]-ish scale is what
// makes a single, isotropic length-scale (lengthScale applied
// identically to every dimension) a reasonable choice rather than one
// dimension silently dominating the Euclidean distance the kernel is
// built from.
func (t *BayesOptTuner) toVector(cfg proxy.AdaptiveConfig) []float64 {
	refRange := float64(t.space.ReferenceLatencyMax - t.space.ReferenceLatencyMin)
	staleRange := float64(t.space.StaleAfterMax - t.space.StaleAfterMin)
	refFrac := float64(cfg.ReferenceLatency-t.space.ReferenceLatencyMin) / refRange
	staleFrac := float64(cfg.StaleAfter-t.space.StaleAfterMin) / staleRange
	return []float64{cfg.Weights.Load, cfg.Weights.Latency, cfg.Weights.Cache, refFrac, staleFrac}
}
