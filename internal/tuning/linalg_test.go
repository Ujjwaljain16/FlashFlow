package tuning

import (
	"math"
	"testing"
)

const linalgTol = 1e-9

func approxEqual(a, b float64) bool { return math.Abs(a-b) < linalgTol }

// TestCholeskyLower_HandComputed2x2 uses A = [[4,2],[2,3]], whose
// Cholesky factor is hand-computable: L11=sqrt(4)=2, L21=2/L11=1,
// L22=sqrt(3-1^2)=sqrt(2). Verified independently by hand before
// writing this test.
func TestCholeskyLower_HandComputed2x2(t *testing.T) {
	a := []float64{4, 2, 2, 3}
	l, err := choleskyLower(a, 2)
	if err != nil {
		t.Fatalf("choleskyLower failed: %v", err)
	}
	want := []float64{2, 0, 1, math.Sqrt(2)}
	for i := range want {
		if !approxEqual(l[i], want[i]) {
			t.Errorf("L[%d] = %v, want %v", i, l[i], want[i])
		}
	}
}

// TestCholeskyLower_ReconstructsOriginalMatrix verifies L*L^T == A for
// a larger, less hand-friendly matrix -- the general correctness
// property any valid Cholesky factor must satisfy, checked directly
// rather than trusting one small hand-computed case to generalize.
func TestCholeskyLower_ReconstructsOriginalMatrix(t *testing.T) {
	n := 4
	a := []float64{
		10, 2, 1, 0.5,
		2, 8, 0.3, 0.2,
		1, 0.3, 6, 0.1,
		0.5, 0.2, 0.1, 5,
	}
	l, err := choleskyLower(a, n)
	if err != nil {
		t.Fatalf("choleskyLower failed: %v", err)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var sum float64
			for k := 0; k < n; k++ {
				sum += l[i*n+k] * l[j*n+k]
			}
			if !approxEqual(sum, a[i*n+j]) {
				t.Errorf("(L*L^T)[%d][%d] = %v, want %v", i, j, sum, a[i*n+j])
			}
		}
	}
}

func TestCholeskyLower_RejectsNonPositiveDefinite(t *testing.T) {
	// [[1,2],[2,1]] has eigenvalues 3 and -1 -- not positive-definite.
	a := []float64{1, 2, 2, 1}
	if _, err := choleskyLower(a, 2); err == nil {
		t.Fatal("expected an error for a non-positive-definite matrix")
	}
}

func TestSolveCholesky_HandComputed(t *testing.T) {
	// A = [[4,2],[2,3]], b = [6,5] -- solve A*x=b by hand:
	// 4x1+2x2=6, 2x1+3x2=5 => x1=1, x2=1 (verify: 4+2=6 ok, 2+3=5 ok).
	a := []float64{4, 2, 2, 3}
	l, err := choleskyLower(a, 2)
	if err != nil {
		t.Fatalf("choleskyLower failed: %v", err)
	}
	x := solveCholesky(l, 2, []float64{6, 5})
	if !approxEqual(x[0], 1) || !approxEqual(x[1], 1) {
		t.Errorf("x = %v, want [1, 1]", x)
	}
}

func TestSolveCholesky_IdentityMatrixReturnsB(t *testing.T) {
	n := 3
	a := []float64{1, 0, 0, 0, 1, 0, 0, 0, 1}
	l, err := choleskyLower(a, n)
	if err != nil {
		t.Fatalf("choleskyLower failed: %v", err)
	}
	b := []float64{7, -3, 2.5}
	x := solveCholesky(l, n, b)
	for i := range b {
		if !approxEqual(x[i], b[i]) {
			t.Errorf("x[%d] = %v, want %v (identity system: x should equal b)", i, x[i], b[i])
		}
	}
}

// TestSolveCholesky_SatisfiesOriginalSystem checks A*x == b for a
// solved system on a larger matrix, the general correctness property,
// independent of any single hand-computed case.
func TestSolveCholesky_SatisfiesOriginalSystem(t *testing.T) {
	n := 4
	a := []float64{
		10, 2, 1, 0.5,
		2, 8, 0.3, 0.2,
		1, 0.3, 6, 0.1,
		0.5, 0.2, 0.1, 5,
	}
	b := []float64{1, 2, 3, 4}
	l, err := choleskyLower(a, n)
	if err != nil {
		t.Fatalf("choleskyLower failed: %v", err)
	}
	x := solveCholesky(l, n, b)
	for i := 0; i < n; i++ {
		var sum float64
		for j := 0; j < n; j++ {
			sum += a[i*n+j] * x[j]
		}
		if !approxEqual(sum, b[i]) {
			t.Errorf("(A*x)[%d] = %v, want %v", i, sum, b[i])
		}
	}
}
