package tuner

import (
	"math"
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	"gonum.org/v1/gonum/mat"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.InitLogger()
}

const epsilon = 1e-6

// TestFloatEqual tests float comparison with epsilon
func TestFloatEqual(t *testing.T) {
	tests := []struct {
		name string
		a    float64
		b    float64
		want bool
	}{
		{
			name: "exactly equal",
			a:    1.0,
			b:    1.0,
			want: true,
		},
		{
			name: "within epsilon",
			a:    1.0,
			b:    1.0 + 1e-10,
			want: true,
		},
		{
			name: "outside epsilon",
			a:    1.0,
			b:    1.1,
			want: false,
		},
		{
			name: "both zero",
			a:    0.0,
			b:    0.0,
			want: true,
		},
		{
			name: "very small difference - relative comparison",
			a:    1.0,
			b:    1.0 + math.SmallestNonzeroFloat64,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FloatEqual(tt.a, tt.b, epsilon); got != tt.want {
				t.Errorf("FloatEqual(%v, %v, %v) = %v, want %v", tt.a, tt.b, epsilon, got, tt.want)
			}
		})
	}
}

// TestIsSymmetric tests matrix symmetry checking
func TestIsSymmetric(t *testing.T) {
	tests := []struct {
		name    string
		matrix  mat.Matrix
		epsilon float64
		want    bool
	}{
		{
			name: "symmetric matrix",
			matrix: mat.NewDense(3, 3, []float64{
				1, 2, 3,
				2, 4, 5,
				3, 5, 6,
			}),
			want: true,
		},
		{
			name: "identity matrix",
			matrix: mat.NewDense(3, 3, []float64{
				1, 0, 0,
				0, 1, 0,
				0, 0, 1,
			}),
			want: true,
		},
		{
			name: "not symmetric",
			matrix: mat.NewDense(3, 3, []float64{
				1, 2, 3,
				4, 5, 6,
				7, 8, 9,
			}),
			want: false,
		},
		{
			name: "not square",
			matrix: mat.NewDense(2, 3, []float64{
				1, 2, 3,
				4, 5, 6,
			}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSymmetric(tt.matrix, tt.epsilon); got != tt.want {
				t.Errorf("IsSymmetric() = %v, want %v", got, tt.want)
			}
		})
	}
}
