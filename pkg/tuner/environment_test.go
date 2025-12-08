package tuner

import (
	"math"
	"testing"

	"gonum.org/v1/gonum/mat"
)

func TestEnvironment_Valid(t *testing.T) {
	tests := []struct {
		name string
		env  *Environment
		want bool
	}{
		{
			name: "all fields valid",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: true,
		},
		{
			name: "zero lambda",
			env: &Environment{
				Lambda:        0.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "negative lambda",
			env: &Environment{
				Lambda:        -10.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "zero input tokens",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  0,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "negative input tokens",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  -100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "zero output tokens",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 0,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "negative output tokens",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: -200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "zero batch size",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  0,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "negative batch size",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  -8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "zero TTFT - invalid",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       0.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "zero ITL - invalid",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        0.0,
			},
			want: false,
		},
		{
			name: "all zeros except observation metrics",
			env: &Environment{
				Lambda:        0.0,
				AvgInputToks:  0,
				AvgOutputToks: 0,
				MaxBatchSize:  0,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			want: false,
		},
		{
			name: "minimal valid environment",
			env: &Environment{
				Lambda:        0.1,
				AvgInputToks:  1,
				AvgOutputToks: 1,
				MaxBatchSize:  1,
				AvgTTFT:       1.0,
				AvgITL:        1.0,
			},
			want: true,
		},
		{
			name: "large values",
			env: &Environment{
				Lambda:        10000.0,
				AvgInputToks:  10000,
				AvgOutputToks: 10000,
				MaxBatchSize:  1000,
				AvgTTFT:       10000.0,
				AvgITL:        1000.0,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.env.Valid(); got != tt.want {
				t.Errorf("Environment.Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnvironment_GetObservations(t *testing.T) {
	tests := []struct {
		name         string
		env          *Environment
		expectedTTFT float64
		expectedITL  float64
	}{
		{
			name: "standard observations",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			expectedTTFT: 150.0,
			expectedITL:  25.0,
		},
		{
			name: "zero observations",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       0.0,
				AvgITL:        0.0,
			},
			expectedTTFT: 0.0,
			expectedITL:  0.0,
		},
		{
			name: "very small observations",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       0.001,
				AvgITL:        0.0001,
			},
			expectedTTFT: 0.001,
			expectedITL:  0.0001,
		},
		{
			name: "very large observations",
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       50000.0,
				AvgITL:        10000.0,
			},
			expectedTTFT: 50000.0,
			expectedITL:  10000.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := tt.env.GetObservations()

			if obs == nil {
				t.Fatal("GetObservations() returned nil")
			}

			if obs.Len() != 2 {
				t.Errorf("GetObservations() returned vector of length %d, want 2", obs.Len())
			}

			ttft := obs.AtVec(0)
			itl := obs.AtVec(1)

			// Use a small epsilon for floating-point comparison
			const epsilon = 1e-9
			if math.Abs(ttft-tt.expectedTTFT) > epsilon {
				t.Errorf("TTFT = %f, want %f", ttft, tt.expectedTTFT)
			}

			if math.Abs(itl-tt.expectedITL) > epsilon {
				t.Errorf("ITL = %f, want %f", itl, tt.expectedITL)
			}
		})
	}
}

func TestEnvironment_FieldTypes(t *testing.T) {
	// Test that environment fields have correct types
	env := &Environment{
		Lambda:        60.5,   // float32
		AvgInputToks:  100,    // int
		AvgOutputToks: 200,    // int
		MaxBatchSize:  8,      // int
		AvgTTFT:       150.25, // float32
		AvgITL:        25.75,  // float32
	}

	// Test that float32 precision is maintained
	if env.Lambda != 60.5 {
		t.Errorf("Lambda = %f, want 60.5", env.Lambda)
	}
	if env.AvgTTFT != 150.25 {
		t.Errorf("AvgTTFT = %f, want 150.25", env.AvgTTFT)
	}
	if env.AvgITL != 25.75 {
		t.Errorf("AvgITL = %f, want 25.75", env.AvgITL)
	}

	// Test that int values are exact
	if env.AvgInputToks != 100 {
		t.Errorf("AvgInputToks = %d, want 100", env.AvgInputToks)
	}
	if env.AvgOutputToks != 200 {
		t.Errorf("AvgOutputToks = %d, want 200", env.AvgOutputToks)
	}
	if env.MaxBatchSize != 8 {
		t.Errorf("MaxBatchSize = %d, want 8", env.MaxBatchSize)
	}
}

func TestEnvironment_Modification(t *testing.T) {
	env := &Environment{
		Lambda:        60.0,
		AvgInputToks:  100,
		AvgOutputToks: 200,
		MaxBatchSize:  8,
		AvgTTFT:       150.0,
		AvgITL:        25.0,
	}

	if !env.Valid() {
		t.Error("Initial environment should be valid")
	}

	// Modify to make invalid
	env.Lambda = 0.0

	if env.Valid() {
		t.Error("Environment should be invalid after setting Lambda to 0")
	}

	// Modify back to valid
	env.Lambda = 120.0

	if !env.Valid() {
		t.Error("Environment should be valid after setting Lambda to positive value")
	}

	// Check that observations updated correctly
	env.AvgTTFT = 200.0
	env.AvgITL = 30.0

	obs := env.GetObservations()
	if obs.AtVec(0) != 200.0 {
		t.Errorf("Modified TTFT = %f, want 200.0", obs.AtVec(0))
	}
	if obs.AtVec(1) != 30.0 {
		t.Errorf("Modified ITL = %f, want 30.0", obs.AtVec(1))
	}
}

func TestEnvironment_BoundaryValues(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value interface{}
		want  bool
	}{
		{"lambda at boundary", "Lambda", float32(0.000001), true},
		{"input tokens at boundary", "AvgInputToks", 1, true},
		{"output tokens at boundary", "AvgOutputToks", 1, true},
		{"batch size at boundary", "MaxBatchSize", 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			}

			switch tt.field {
			case "Lambda":
				env.Lambda = tt.value.(float32)
			case "AvgInputToks":
				env.AvgInputToks = tt.value.(int)
			case "AvgOutputToks":
				env.AvgOutputToks = tt.value.(int)
			case "MaxBatchSize":
				env.MaxBatchSize = tt.value.(int)
			}

			if got := env.Valid(); got != tt.want {
				t.Errorf("Environment.Valid() with %s=%v = %v, want %v", tt.field, tt.value, got, tt.want)
			}
		})
	}
}

func TestEnvironment_GetObservations_VectorType(t *testing.T) {
	env := &Environment{
		Lambda:        60.0,
		AvgInputToks:  100,
		AvgOutputToks: 200,
		MaxBatchSize:  8,
		AvgTTFT:       150.0,
		AvgITL:        25.0,
	}

	obs := env.GetObservations()

	// Test that it returns a proper VecDense
	if obs == nil {
		t.Fatal("GetObservations() returned nil")
	}

	// Test that we can perform vector operations on it
	doubled := mat.NewVecDense(2, nil)
	doubled.ScaleVec(2.0, obs)

	if doubled.AtVec(0) != 300.0 {
		t.Errorf("Scaled TTFT = %f, want 300.0", doubled.AtVec(0))
	}
	if doubled.AtVec(1) != 50.0 {
		t.Errorf("Scaled ITL = %f, want 50.0", doubled.AtVec(1))
	}
}
