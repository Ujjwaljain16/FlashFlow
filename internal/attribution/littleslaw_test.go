package attribution

import (
	"math"
	"testing"
)

func TestCheckLittlesLaw_ExactMatch(t *testing.T) {
	// Hand-computed: lambda=10 req/s, W=0.5s => predicted L = 5. Giving
	// an observed L of exactly 5 should report zero error.
	got, err := CheckLittlesLaw(Sample{L: 5, Lambda: 10, W: 0.5})
	if err != nil {
		t.Fatalf("CheckLittlesLaw failed: %v", err)
	}
	if got.Predicted != 5 {
		t.Errorf("Predicted = %v, want 5", got.Predicted)
	}
	if got.Observed != 5 {
		t.Errorf("Observed = %v, want 5", got.Observed)
	}
	if got.AbsError != 0 || got.RelError != 0 {
		t.Errorf("expected zero error for an exact match, got AbsError=%v RelError=%v", got.AbsError, got.RelError)
	}
}

func TestCheckLittlesLaw_HandComputedMismatch(t *testing.T) {
	// lambda=10, W=0.5 => predicted=5; observed L=4 => AbsError=+1,
	// RelError=1/4=0.25.
	got, err := CheckLittlesLaw(Sample{L: 4, Lambda: 10, W: 0.5})
	if err != nil {
		t.Fatalf("CheckLittlesLaw failed: %v", err)
	}
	if math.Abs(got.AbsError-1) > 1e-9 {
		t.Errorf("AbsError = %v, want 1", got.AbsError)
	}
	if math.Abs(got.RelError-0.25) > 1e-9 {
		t.Errorf("RelError = %v, want 0.25", got.RelError)
	}
}

func TestCheckLittlesLaw_ZeroObservedAvoidsDivideByZero(t *testing.T) {
	got, err := CheckLittlesLaw(Sample{L: 0, Lambda: 0, W: 0})
	if err != nil {
		t.Fatalf("CheckLittlesLaw failed: %v", err)
	}
	if got.RelError != 0 {
		t.Errorf("RelError = %v, want 0 when Observed is exactly 0", got.RelError)
	}
}

func TestCheckLittlesLaw_RejectsNegativeInputs(t *testing.T) {
	cases := []Sample{
		{L: -1, Lambda: 1, W: 1},
		{L: 1, Lambda: -1, W: 1},
		{L: 1, Lambda: 1, W: -1},
	}
	for _, s := range cases {
		if _, err := CheckLittlesLaw(s); err == nil {
			t.Errorf("CheckLittlesLaw(%+v): expected an error for a negative input", s)
		}
	}
}
