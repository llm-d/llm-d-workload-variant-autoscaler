package tuner

import (
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"gonum.org/v1/gonum/mat"
)

func TestNewConfigurator(t *testing.T) {
	tests := []struct {
		name       string
		configData *TunerConfigData
		wantErr    bool
	}{
		{
			name: "valid configuration",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: constants.DefaultGammaFactor,
					ErrorLevel:  constants.DefaultErrorLevel,
					TPercentile: constants.DefaultTPercentile,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 10.0, 1.5},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         true,
					MinState:             []float64{0.5, 0.2, 0.15, 1.0},
					MaxState:             []float64{50.0, 20.0, 15.0, 100.0},
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: false,
		},
		{
			name:       "nil config data",
			configData: nil,
			wantErr:    true,
		},
		{
			name: "empty init state",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{},
					PercentChange:        []float64{},
					BoundedState:         false,
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: true,
		},
		{
			name: "mismatched dimensions",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0},
					PercentChange:        []float64{0.05, 0.05, 0.05}, // Mismatch
					BoundedState:         false,
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: true,
		},
		{
			name: "bounded with incomplete min bounds",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 10.0, 1.5},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         true,
					MinState:             []float64{0.5, 0.2}, // Incomplete
					MaxState:             []float64{50.0, 20.0, 15.0, 100.0},
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: true,
		},
		{
			name: "bounded with incomplete max bounds",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 10.0, 1.5},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         true,
					MinState:             []float64{0.5, 0.2, 0.15, 1.0},
					MaxState:             []float64{50.0, 20.0}, // Incomplete
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: true,
		},
		{
			name: "empty expected observations",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 10.0, 1.5},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         false,
					ExpectedObservations: []float64{}, // Empty
				},
			},
			wantErr: true,
		},
		{
			name: "unbounded state with valid dimensions",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 10.0, 1.5},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         false,
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewConfigurator(tt.configData)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigurator() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && c == nil {
				t.Error("NewConfigurator() returned nil without error")
			}
		})
	}
}

func TestConfigurator_NumStates(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.0, 10.0, 1.5},
			PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
			BoundedState:         false,
			ExpectedObservations: []float64{150.0, 25.0},
		},
	}

	c, err := NewConfigurator(configData)
	if err != nil {
		t.Fatalf("NewConfigurator() failed: %v", err)
	}

	numStates := c.NumStates()
	if numStates != 4 {
		t.Errorf("NumStates() = %d, want 4", numStates)
	}
}

func TestConfigurator_NumObservations(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.0, 10.0, 1.5},
			PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
			BoundedState:         false,
			ExpectedObservations: []float64{150.0, 25.0},
		},
	}

	c, err := NewConfigurator(configData)
	if err != nil {
		t.Fatalf("NewConfigurator() failed: %v", err)
	}

	numObs := c.NumObservations()
	if numObs != 2 {
		t.Errorf("NumObservations() = %d, want 2", numObs)
	}
}

func TestConfigurator_GetStateCov(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.0, 10.0, 1.5},
			PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
			BoundedState:         false,
			ExpectedObservations: []float64{150.0, 25.0},
		},
	}

	c, err := NewConfigurator(configData)
	if err != nil {
		t.Fatalf("NewConfigurator() failed: %v", err)
	}

	x := mat.NewVecDense(4, []float64{5.0, 2.0, 10.0, 1.5})
	cov, err := c.GetStateCov(x)
	if err != nil {
		t.Errorf("GetStateCov() failed: %v", err)
	}

	if cov == nil {
		t.Fatal("GetStateCov() returned nil")
	}

	rows, cols := cov.Dims()
	if rows != 4 || cols != 4 {
		t.Errorf("GetStateCov() returned %dx%d matrix, want 4x4", rows, cols)
	}

	// Check that it's a diagonal matrix with positive values
	for i := range 4 {
		for j := range 4 {
			val := cov.At(i, j)
			if i == j {
				if val <= 0 {
					t.Errorf("Diagonal element [%d,%d] = %f, want positive", i, j, val)
				}
			} else {
				if val != 0 {
					t.Errorf("Off-diagonal element [%d,%d] = %f, want 0", i, j, val)
				}
			}
		}
	}
}

func TestConfigurator_GetStateCov_InvalidDimension(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.0, 10.0, 1.5},
			PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
			BoundedState:         false,
			ExpectedObservations: []float64{150.0, 25.0},
		},
	}

	c, err := NewConfigurator(configData)
	if err != nil {
		t.Fatalf("NewConfigurator() failed: %v", err)
	}

	// Wrong dimension
	x := mat.NewVecDense(2, []float64{5.0, 2.0})
	_, err = c.GetStateCov(x)
	if err == nil {
		t.Error("GetStateCov() should fail with wrong dimension")
	}
}

func TestConfigurator_MatrixDimensions(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.0, 10.0, 1.5},
			PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
			BoundedState:         false,
			ExpectedObservations: []float64{150.0, 25.0},
		},
	}

	c, err := NewConfigurator(configData)
	if err != nil {
		t.Fatalf("NewConfigurator() failed: %v", err)
	}

	// The configurator should expose X0, P, Q, R through the created tuner
	// Check dimensions indirectly through NumStates and NumObservations
	nStates := c.NumStates()
	nObs := c.NumObservations()

	if nStates != 4 {
		t.Errorf("Expected 4 states, got %d", nStates)
	}
	if nObs != 2 {
		t.Errorf("Expected 2 observations, got %d", nObs)
	}
}

func TestConfigurator_CovarianceMatrixSymmetry(t *testing.T) {
	tests := []struct {
		name                 string
		initCovarianceMatrix []float64
		configData           *TunerConfigData
		wantErr              bool
	}{
		{
			name: "symmetric covariance matrix",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState: []float64{5.0, 2.0, 10.0, 1.5},
					InitCovarianceMatrix: []float64{1.0, 0.0, 0.02, 0.01,
						0.0, 1.0, 0.0, 0.02,
						0.02, 0.0, 1.0, 0.0,
						0.01, 0.02, 0.0, 1.0},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         false,
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: false,
		},
		{
			name: "not symmetric covariance matrix",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState: []float64{5.0, 2.0, 10.0, 1.5},
					InitCovarianceMatrix: []float64{0.1, 0.01, 0.0, 0.0,
						0.02, 0.1, 0.0, 0.0,
						0.0, 0.0, 0.1, 0.0,
						0.0, 0.0, 0.0, 0.1},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         false,
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewConfigurator(tt.configData)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigurator() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && c == nil {
				t.Error("NewConfigurator() returned nil without error")
			}
		})
	}
}

func TestConfigurator_VariousStateAndObsDimensions(t *testing.T) {
	tests := []struct {
		name      string
		numStates int
		numObs    int
		wantErr   bool
	}{
		{
			name:      "standard 4 states, 2 observations",
			numStates: 4,
			numObs:    2,
			wantErr:   false,
		},
		{
			name:      "minimal 1 state, 1 observation",
			numStates: 1,
			numObs:    1,
			wantErr:   false,
		},
		{
			name:      "larger 6 states, 3 observations",
			numStates: 6,
			numObs:    3,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initState := make([]float64, tt.numStates)
			percentChange := make([]float64, tt.numStates)
			expectedObs := make([]float64, tt.numObs)

			for i := 0; i < tt.numStates; i++ {
				initState[i] = float64(i + 1)
				percentChange[i] = 0.05
			}
			for i := 0; i < tt.numObs; i++ {
				expectedObs[i] = float64((i + 1) * 100)
			}

			configData := &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            initState,
					PercentChange:        percentChange,
					BoundedState:         false,
					ExpectedObservations: expectedObs,
				},
			}

			c, err := NewConfigurator(configData)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigurator() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if c.NumStates() != tt.numStates {
					t.Errorf("NumStates() = %d, want %d", c.NumStates(), tt.numStates)
				}
				if c.NumObservations() != tt.numObs {
					t.Errorf("NumObservations() = %d, want %d", c.NumObservations(), tt.numObs)
				}
			}
		})
	}
}

// TestConfigurator_ErrorPaths tests error handling in configurator
func TestConfigurator_ErrorPaths(t *testing.T) {
	t.Run("empty_percent_change", func(t *testing.T) {
		configData := &TunerConfigData{
			FilterData: FilterData{
				GammaFactor: 1.0,
				ErrorLevel:  0.05,
				TPercentile: 1.96,
			},
			ModelData: TunerModelData{
				InitState:            []float64{5.0, 2.5, 10.0, 0.15},
				PercentChange:        []float64{}, // Empty
				BoundedState:         false,
				ExpectedObservations: []float64{190, 15.0},
			},
		}

		_, err := NewConfigurator(configData)
		if err == nil {
			t.Error("Expected error with empty PercentChange")
		}
	})

	t.Run("empty_expected_observations", func(t *testing.T) {
		configData := &TunerConfigData{
			FilterData: FilterData{
				GammaFactor: 1.0,
				ErrorLevel:  0.05,
				TPercentile: 1.96,
			},
			ModelData: TunerModelData{
				InitState:            []float64{5.0, 2.5, 10.0, 0.15},
				PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
				BoundedState:         false,
				ExpectedObservations: []float64{}, // Empty
			},
		}

		_, err := NewConfigurator(configData)
		if err == nil {
			t.Error("Expected error with empty ExpectedObservations")
		}
	})

	t.Run("bounded_state_missing_min", func(t *testing.T) {
		configData := &TunerConfigData{
			FilterData: FilterData{
				GammaFactor: 1.0,
				ErrorLevel:  0.05,
				TPercentile: 1.96,
			},
			ModelData: TunerModelData{
				InitState:            []float64{5.0, 2.5, 10.0, 0.15},
				PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
				BoundedState:         true,
				MinState:             []float64{1.0, 0.5}, // Missing entries
				MaxState:             []float64{20.0, 10.0, 50.0, 1.0},
				ExpectedObservations: []float64{190, 15.0},
			},
		}

		_, err := NewConfigurator(configData)
		if err == nil {
			t.Error("Expected error with incomplete MinState bounds")
		}
	})
}
