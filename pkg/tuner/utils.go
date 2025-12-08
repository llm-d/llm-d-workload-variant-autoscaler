package tuner

import (
	"math"

	"gonum.org/v1/gonum/mat"
)

// infinitesimal value to check float equality
const DefaultEpsilon = 1e-6

// FloatEqual checks if two float64 numbers are approximately equal within a given epsilon.
func FloatEqual(a, b, epsilon float64) bool {
	// Handle the case where they are exactly equal.
	if a == b {
		return true
	}

	// Calculate the absolute difference.
	diff := math.Abs(a - b)

	// Compare the absolute difference with a combination of absolute and relative tolerance.
	// This helps handle cases with very small or very large numbers.
	if a == 0.0 || b == 0.0 || diff < math.SmallestNonzeroFloat64 {
		return diff < (epsilon * math.SmallestNonzeroFloat64)
	}
	return diff/(math.Abs(a)+math.Abs(b)) < epsilon
}

// IsSymmetric checks if a given mat.Matrix is symmetric.
func IsSymmetric(m mat.Matrix, epsilon float64) bool {
	r, c := m.Dims()

	// 1. Check if it's a square matrix
	if r != c {
		return false
	}

	// 2. Check if elements are equal to their transposes
	// We only need to check the upper or lower triangle (excluding the diagonal)
	// because if a_ij = a_ji, then a_ji = a_ij is also true.
	for i := 0; i < r; i++ {
		for j := i + 1; j < c; j++ { // Start from j = i + 1 to avoid checking diagonal and duplicates
			if !FloatEqual(m.At(i, j), m.At(j, i), epsilon) {
				return false
			}
		}
	}

	return true
}
