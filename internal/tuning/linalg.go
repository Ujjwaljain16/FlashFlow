package tuning

import (
	"fmt"
	"math"
)

// choleskyLower computes the lower-triangular Cholesky factor L such
// that A = L*L^T, for a symmetric positive-definite n x n matrix A
// stored row-major as a flat []float64 of length n*n -- the standard
// in-place Cholesky-Banachiewicz algorithm, hand-rolled per Stage 10's
// confirmed decision to avoid a linear-algebra dependency (e.g.
// gonum) for the one place this project's Bayesian Optimization
// implementation needs matrix factorization.
//
// Returns an error rather than a garbage result if A is not (numerically)
// positive-definite -- a diagonal term that comes out zero or negative
// after subtracting off already-computed contributions means A wasn't
// PD to begin with, or is right at the edge of floating-point
// precision (e.g. two literally identical observed points in the GP's
// training set, which makes their kernel row/column linearly
// dependent). Silently proceeding (e.g. clamping to a small positive
// value) would produce a factorization that LOOKS valid but corrupts
// every downstream prediction without any visible symptom -- exactly
// the kind of plausible-but-wrong result this project's own Stage 8
// lesson (a measurement tool is part of the system under test) says to
// guard against, not just for load generators but for hand-rolled math
// too.
func choleskyLower(a []float64, n int) ([]float64, error) {
	l := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := a[i*n+j]
			for k := 0; k < j; k++ {
				sum -= l[i*n+k] * l[j*n+k]
			}
			if i == j {
				if sum <= 0 {
					return nil, fmt.Errorf("tuning: matrix is not positive-definite (diagonal term %.6g at row %d)", sum, i)
				}
				l[i*n+j] = math.Sqrt(sum)
			} else {
				l[i*n+j] = sum / l[j*n+j]
			}
		}
	}
	return l, nil
}

// forwardSubstitute solves L*x = b for x, where l is an n x n lower-
// triangular matrix stored row-major.
func forwardSubstitute(l []float64, n int, b []float64) []float64 {
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := b[i]
		for k := 0; k < i; k++ {
			sum -= l[i*n+k] * x[k]
		}
		x[i] = sum / l[i*n+i]
	}
	return x
}

// backSubstituteTransposed solves L^T*x = b for x, without ever
// explicitly forming L^T -- l[k*n+i] is L^T's [i][k] entry (= L's
// [k][i] entry), read directly from l's own row-major storage.
func backSubstituteTransposed(l []float64, n int, b []float64) []float64 {
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := b[i]
		for k := i + 1; k < n; k++ {
			sum -= l[k*n+i] * x[k]
		}
		x[i] = sum / l[i*n+i]
	}
	return x
}

// solveCholesky solves A*x = b given A's lower Cholesky factor l (where
// A = L*L^T) via forward substitution (L*y = b) then back substitution
// (L^T*x = y) -- the standard two-step Cholesky solve, which never
// forms A^-1 explicitly (computing an explicit inverse is both more
// expensive and less numerically stable than solving directly).
func solveCholesky(l []float64, n int, b []float64) []float64 {
	y := forwardSubstitute(l, n, b)
	return backSubstituteTransposed(l, n, y)
}
