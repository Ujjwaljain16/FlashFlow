package tuning

import (
	"math"
	"testing"
)

func TestKernel_SelfSimilarityEqualsSignalVariance(t *testing.T) {
	x := []float64{0.3, 0.5, 0.1, 0.7, 0.2}
	got := kernel(x, x, 0.3, 1.0)
	if !approxEqual(got, 1.0) {
		t.Errorf("kernel(x,x) = %v, want signalVar (1.0) since sumSq=0", got)
	}
}

func TestKernel_DecaysWithDistance(t *testing.T) {
	x1 := []float64{0, 0}
	near := []float64{0.01, 0}
	far := []float64{5, 5}
	kNear := kernel(x1, near, 0.3, 1.0)
	kFar := kernel(x1, far, 0.3, 1.0)
	if kNear <= kFar {
		t.Errorf("expected kernel(near) > kernel(far), got near=%v far=%v", kNear, kFar)
	}
	if kFar < 0 || kFar > 0.001 {
		t.Errorf("expected a far-apart kernel value near 0, got %v", kFar)
	}
}

// TestFitGP_InterpolatesTrainingData is the standard GP sanity check:
// with tiny observation noise, the posterior mean at an already-
// observed point must closely match that point's own training value,
// and the posterior variance there must be small (the model is
// confident about points it has directly seen).
func TestFitGP_InterpolatesTrainingData(t *testing.T) {
	x := [][]float64{
		{0.1, 0.1, 0.1, 0.1, 0.1},
		{0.5, 0.5, 0.5, 0.5, 0.5},
		{0.9, 0.2, 0.3, 0.4, 0.1},
	}
	y := []float64{0.3, 0.7, 0.5}

	gp, err := fitGP(x, y, 0.3, 1.0, 1e-6)
	if err != nil {
		t.Fatalf("fitGP failed: %v", err)
	}

	for i, xi := range x {
		mean, stddev := gp.predict(xi)
		if math.Abs(mean-y[i]) > 0.01 {
			t.Errorf("point %d: predicted mean %v, want close to training value %v", i, mean, y[i])
		}
		if stddev > 0.05 {
			t.Errorf("point %d: predicted stddev %v, want small (near-zero) at an observed point", i, stddev)
		}
	}
}

func TestPredict_HigherUncertaintyFarFromTrainingData(t *testing.T) {
	x := [][]float64{{0.1, 0.1, 0.1, 0.1, 0.1}}
	y := []float64{0.5}
	gp, err := fitGP(x, y, 0.3, 1.0, 1e-6)
	if err != nil {
		t.Fatalf("fitGP failed: %v", err)
	}

	_, stddevNear := gp.predict([]float64{0.11, 0.1, 0.1, 0.1, 0.1})
	_, stddevFar := gp.predict([]float64{0.9, 0.9, 0.9, 0.9, 0.9})
	if stddevFar <= stddevNear {
		t.Errorf("expected higher uncertainty far from the only training point, got near=%v far=%v", stddevNear, stddevFar)
	}
}

func TestExpectedImprovement_ZeroWhenCertainAndNotBetter(t *testing.T) {
	if got := expectedImprovement(0.4, 0, 0.5, 0.01); got != 0 {
		t.Errorf("expectedImprovement(mean=0.4, stddev=0, best=0.5) = %v, want 0", got)
	}
}

func TestExpectedImprovement_PositiveWhenCertainAndBetter(t *testing.T) {
	got := expectedImprovement(0.8, 0, 0.5, 0)
	if !approxEqual(got, 0.3) {
		t.Errorf("expectedImprovement(mean=0.8, stddev=0, best=0.5, xi=0) = %v, want 0.3 (mean-best)", got)
	}
}

// TestExpectedImprovement_HandComputedAtEquality: mean=best, xi=0 =>
// Z=0, EI = 0*Phi(0) + stddev*phi(0) = stddev/sqrt(2*pi) -- a standard,
// hand-verifiable closed form for this specific case.
func TestExpectedImprovement_HandComputedAtEquality(t *testing.T) {
	stddev := 1.0
	got := expectedImprovement(0.5, stddev, 0.5, 0)
	want := stddev / math.Sqrt(2*math.Pi)
	if !approxEqual(got, want) {
		t.Errorf("expectedImprovement at mean==best = %v, want %v", got, want)
	}
}

func TestExpectedImprovement_IncreasesWithStddevAtEquality(t *testing.T) {
	lowStddev := expectedImprovement(0.5, 0.1, 0.5, 0)
	highStddev := expectedImprovement(0.5, 1.0, 0.5, 0)
	if highStddev <= lowStddev {
		t.Errorf("expected EI to increase with stddev when mean==best, got low=%v high=%v", lowStddev, highStddev)
	}
}

func TestBayesOptTuner_FallsBackToRandomWithFewerThanTwoTrials(t *testing.T) {
	tuner := NewBayesOptTuner(1, DefaultConfigSpace())
	cfg := tuner.Suggest(nil)
	if ok, reason := tuner.Space().Valid(cfg); !ok {
		t.Errorf("Suggest(nil) produced an invalid config: %s", reason)
	}
	cfg2 := tuner.Suggest([]TrialResult{{Config: cfg, Utility: 0.5, Valid: true}})
	if ok, reason := tuner.Space().Valid(cfg2); !ok {
		t.Errorf("Suggest with 1 prior trial produced an invalid config: %s", reason)
	}
}

func TestBayesOptTuner_AlwaysSuggestsValidConfigs(t *testing.T) {
	tuner := NewBayesOptTuner(42, DefaultConfigSpace())
	var previous []TrialResult
	for i := 0; i < 30; i++ {
		cfg := tuner.Suggest(previous)
		if ok, reason := tuner.Space().Valid(cfg); !ok {
			t.Fatalf("iteration %d: Suggest produced an invalid config: %s (%+v)", i, reason, cfg)
		}
		// Alternate valid/invalid utility feedback to also exercise the
		// "skip invalid trials" path in Suggest's own filtering.
		previous = append(previous, TrialResult{Config: cfg, Utility: float64(i) / 30.0, Valid: i%5 != 0})
	}
}

func TestBayesOptTuner_Determinism(t *testing.T) {
	build := func() []float64 {
		tuner := NewBayesOptTuner(7, DefaultConfigSpace())
		var previous []TrialResult
		var utilities []float64
		for i := 0; i < 10; i++ {
			cfg := tuner.Suggest(previous)
			u := float64(i) * 0.05
			utilities = append(utilities, u)
			previous = append(previous, TrialResult{Config: cfg, Utility: u, Valid: true})
		}
		return utilities
	}
	a := build()
	b := build()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same-seed BayesOptTuner runs diverged at trial %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestBayesOptTuner_Interface(t *testing.T) {
	var _ Tuner = NewBayesOptTuner(1, DefaultConfigSpace())
}
