package tuner

import (
	"math"
	"strconv"
	"testing"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/analyzer"
	infernoConfig "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	tune "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/tuner"
	"gonum.org/v1/gonum/mat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	// Initialize logger for tests
	_, _ = logger.InitLogger()
}

const epsilon = 1e-6

// TestGetStateAndCovariance tests state extraction from VA with fallback logic
func TestGetStateAndCovariance(t *testing.T) {
	systemData := &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			Models: infernoConfig.ModelData{
				PerfData: []infernoConfig.ModelAcceleratorPerfData{
					{
						Name:     "llama-3-8b",
						Acc:      "A100",
						AccCount: 1,
						DecodeParms: infernoConfig.DecodeParms{
							Alpha: 5.0,
							Beta:  2.5,
						},
						PrefillParms: infernoConfig.PrefillParms{
							Gamma: 10.0,
							Delta: 0.15,
						},
					},
				},
			},
		},
	}

	server := &infernoConfig.ServerSpec{
		Model: "llama-3-8b",
		CurrentAlloc: infernoConfig.AllocationData{
			Accelerator: "A100",
		},
	}

	tests := []struct {
		name      string
		va        *llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		wantErr   bool
		wantAlpha float64
		wantBeta  float64
	}{
		{
			name: "with tuned results in status",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms: map[string]string{
								"alpha": "6.0",
								"beta":  "3.0",
							},
							PrefillParms: map[string]string{
								"gamma": "12.0",
								"delta": "0.2",
							},
						},
						CovarianceMatrix: [][]string{
							{"0.1", "0", "0", "0"},
							{"0", "0.1", "0", "0"},
							{"0", "0", "0.1", "0"},
							{"0", "0", "0", "0.1"},
						},
					},
				},
			},
			wantErr:   false,
			wantAlpha: 6.0,
			wantBeta:  3.0,
		},
		{
			name: "no tuned results - fallback to spec",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{},
			},
			wantErr:   false,
			wantAlpha: 5.0,
			wantBeta:  2.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, _, err := getStateAndCovariance(tt.va, systemData, server, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("getStateAndCovariance() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if state[constants.StateIndexAlpha] != tt.wantAlpha {
					t.Errorf("alpha = %v, want %v", state[constants.StateIndexAlpha], tt.wantAlpha)
				}
				if state[constants.StateIndexBeta] != tt.wantBeta {
					t.Errorf("beta = %v, want %v", state[constants.StateIndexBeta], tt.wantBeta)
				}
			}
		})
	}
}

// TestExtractCovarianceFromVAStatus tests covariance matrix extraction
func TestExtractCovarianceFromVAStatus(t *testing.T) {
	tests := []struct {
		name    string
		va      *llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		wantErr bool
	}{
		{
			name: "valid 4x4 symmetric matrix",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{Name: "test-va", Namespace: "default"},
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						CovarianceMatrix: [][]string{
							{"0.1", "0.01", "0", "0"},
							{"0.01", "0.1", "0", "0"},
							{"0", "0", "0.1", "0.01"},
							{"0", "0", "0.01", "0.1"},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - not symmetric",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{Name: "test-va", Namespace: "default"},
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						CovarianceMatrix: [][]string{
							{"0.1", "0.02", "0", "0"}, // Note: 0.02 here
							{"0.01", "0.1", "0", "0"}, // but 0.01 here - asymmetric!
							{"0", "0", "0.1", "0.01"},
							{"0", "0", "0.01", "0.1"},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid dimensions - not square",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{Name: "test-va", Namespace: "default"},
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						CovarianceMatrix: [][]string{
							{"0.1", "0", "0"},
							{"0", "0.1", "0"},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid format - non-numeric string",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{Name: "test-va", Namespace: "default"},
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						CovarianceMatrix: [][]string{
							{"0.1", "0", "0", "0"},
							{"0", "invalid", "0", "0"},
							{"0", "0", "0.1", "0"},
							{"0", "0", "0", "0.1"},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix, err := extractCovarianceFromVAStatus(tt.va)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractCovarianceFromVAStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && matrix == nil {
				t.Error("extractCovarianceFromVAStatus() returned nil matrix without error")
			}
		})
	}
}

// TestExtractStateFromVAStatus tests state parameter extraction
func TestExtractStateFromVAStatus(t *testing.T) {
	tests := []struct {
		name    string
		va      *llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		wantErr bool
		want    []float64
	}{
		{
			name: "valid state parameters",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms: map[string]string{
								"alpha": "5.5",
								"beta":  "2.8",
							},
							PrefillParms: map[string]string{
								"gamma": "11.0",
								"delta": "0.16",
							},
						},
					},
				},
			},
			wantErr: false,
			want:    []float64{5.5, 2.8, 11.0, 0.16},
		},
		{
			name: "invalid decode params count",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms: map[string]string{
								"alpha": "5.5",
							},
							PrefillParms: map[string]string{
								"gamma": "11.0",
								"delta": "0.16",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid number format",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms: map[string]string{
								"alpha": "not_a_number",
								"beta":  "2.8",
							},
							PrefillParms: map[string]string{
								"gamma": "11.0",
								"delta": "0.16",
							},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractStateFromVAStatus(tt.va)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractStateFromVAStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("extractStateFromVAStatus() length = %v, want %v", len(got), len(tt.want))
					return
				}
				// Use epsilon for float comparison due to precision
				for i := range got {
					if math.Abs(got[i]-tt.want[i]) > epsilon {
						t.Errorf("extractStateFromVAStatus()[%d] = %v, want %v", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

// TestFindStateInSystemData tests initial state lookup
func TestFindStateInSystemData(t *testing.T) {
	systemData := &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			Models: infernoConfig.ModelData{
				PerfData: []infernoConfig.ModelAcceleratorPerfData{
					{
						Name:     "llama-3-8b",
						Acc:      "A100",
						AccCount: 1,
						DecodeParms: infernoConfig.DecodeParms{
							Alpha: 5.0,
							Beta:  2.5,
						},
						PrefillParms: infernoConfig.PrefillParms{
							Gamma: 10.0,
							Delta: 0.15,
						},
					},
					{
						Name:     "llama-3-70b",
						Acc:      "H100",
						AccCount: 2,
						DecodeParms: infernoConfig.DecodeParms{
							Alpha: 8.0,
							Beta:  3.0,
						},
						PrefillParms: infernoConfig.PrefillParms{
							Gamma: 15.0,
							Delta: 0.2,
						},
					},
					{
						Name:     "invalid-model",
						Acc:      "A100",
						AccCount: 1,
						DecodeParms: infernoConfig.DecodeParms{
							Alpha: -1.0, // Invalid: negative
							Beta:  2.5,
						},
						PrefillParms: infernoConfig.PrefillParms{
							Gamma: 10.0,
							Delta: 0.15,
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name            string
		modelName       string
		acceleratorName string
		wantErr         bool
		wantAlpha       float64
	}{
		{
			name:            "existing model and accelerator",
			modelName:       "llama-3-8b",
			acceleratorName: "A100",
			wantErr:         false,
			wantAlpha:       5.0,
		},
		{
			name:            "different model",
			modelName:       "llama-3-70b",
			acceleratorName: "H100",
			wantErr:         false,
			wantAlpha:       8.0,
		},
		{
			name:            "non-existent model",
			modelName:       "non-existent",
			acceleratorName: "A100",
			wantErr:         true,
		},
		{
			name:            "invalid parameters",
			modelName:       "invalid-model",
			acceleratorName: "A100",
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := findStateInSystemData(systemData, tt.modelName, tt.acceleratorName)
			if (err != nil) != tt.wantErr {
				t.Errorf("findStateInSystemData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && state[constants.StateIndexAlpha] != tt.wantAlpha {
				t.Errorf("alpha = %v, want %v", state[constants.StateIndexAlpha], tt.wantAlpha)
			}
		})
	}
}

// TestDenseMatrixToSliceOfStrings tests matrix to string conversion
func TestDenseMatrixToSliceOfStrings(t *testing.T) {
	tests := []struct {
		name     string
		matrix   *mat.Dense
		wantRows int
		wantCols int
	}{
		{
			name: "2x2 matrix",
			matrix: mat.NewDense(2, 2, []float64{
				1.0, 2.0,
				3.0, 4.0,
			}),
			wantRows: 2,
			wantCols: 2,
		},
		{
			name: "4x4 identity matrix",
			matrix: mat.NewDense(4, 4, []float64{
				1, 0, 0, 0,
				0, 1, 0, 0,
				0, 0, 1, 0,
				0, 0, 0, 1,
			}),
			wantRows: 4,
			wantCols: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := denseMatrixToSliceOfStrings(tt.matrix)
			if len(result) != tt.wantRows {
				t.Errorf("rows = %v, want %v", len(result), tt.wantRows)
			}
			if len(result) > 0 && len(result[0]) != tt.wantCols {
				t.Errorf("cols = %v, want %v", len(result[0]), tt.wantCols)
			}
		})
	}
}

// TestGetDefaultFilterData tests filter data defaults
func TestGetDefaultFilterData(t *testing.T) {
	filterData := getDefaultFilterData()

	if filterData.GammaFactor != constants.DefaultGammaFactor {
		t.Errorf("GammaFactor = %v, want %v", filterData.GammaFactor, constants.DefaultGammaFactor)
	}
	if filterData.ErrorLevel != constants.DefaultErrorLevel {
		t.Errorf("ErrorLevel = %v, want %v", filterData.ErrorLevel, constants.DefaultErrorLevel)
	}
	if filterData.TPercentile != constants.DefaultTPercentile {
		t.Errorf("TPercentile = %v, want %v", filterData.TPercentile, constants.DefaultTPercentile)
	}
}

// TestGetDefaultPercentChange tests default percent change values
func TestGetDefaultPercentChange(t *testing.T) {
	percentChange := getDefaultPercentChange()

	if len(percentChange) != 4 {
		t.Errorf("percentChange length = %v, want 4", len(percentChange))
	}

	for i, val := range percentChange {
		if val != constants.DefaultPercentChange {
			t.Errorf("percentChange[%d] = %v, want %v", i, val, constants.DefaultPercentChange)
		}
	}
}

// TestBuildTunerConfig tests the configuration builder for tuner
func TestBuildTunerConfig(t *testing.T) {
	tests := []struct {
		name      string
		state     []float64
		covMatrix []float64
		slos      []float64
		wantErr   bool
	}{
		{
			name:      "valid inputs",
			state:     []float64{5.0, 2.5, 10.0, 0.15},
			covMatrix: nil,
			slos:      []float64{150.0, 25.0},
			wantErr:   false,
		},
		{
			name:      "with covariance matrix",
			state:     []float64{5.0, 2.5, 10.0, 0.15},
			covMatrix: make([]float64, 16),
			slos:      []float64{150.0, 25.0},
			wantErr:   false,
		},
		{
			name:      "empty state",
			state:     []float64{},
			covMatrix: nil,
			slos:      []float64{150.0, 25.0},
			wantErr:   true,
		},
		{
			name:      "empty slos",
			state:     []float64{5.0, 2.5, 10.0, 0.15},
			covMatrix: nil,
			slos:      []float64{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := BuildTunerConfig(tt.state, tt.covMatrix, tt.slos)
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildTunerConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if config == nil {
					t.Error("BuildTunerConfig() returned nil config without error")
					return
				}
				if len(config.ModelData.InitState) != len(tt.state) {
					t.Errorf("State length = %d, want %d", len(config.ModelData.InitState), len(tt.state))
				}
				if len(config.ModelData.ExpectedObservations) != len(tt.slos) {
					t.Errorf("ExpectedObservations length = %d, want %d", len(config.ModelData.ExpectedObservations), len(tt.slos))
				}
				if !config.ModelData.BoundedState {
					t.Error("Expected BoundedState to be true")
				}
			}
		})
	}
}

// TestConvertAllocToEnvironment tests allocation to environment conversion adapter
func TestConvertAllocToEnvironment(t *testing.T) {
	tests := []struct {
		name         string
		alloc        infernoConfig.AllocationData
		wantLambda   float32
		wantReplicas int
		wantErr      bool
	}{
		{
			name: "normal allocation",
			alloc: infernoConfig.AllocationData{
				NumReplicas: 2,
				MaxBatch:    8,
				Load: infernoConfig.ServerLoadSpec{
					ArrivalRate:  120.0,
					AvgInTokens:  100,
					AvgOutTokens: 200,
				},
				TTFTAverage: 150.0,
				ITLAverage:  25.0,
			},
			wantLambda:   60.0, // 120/2
			wantReplicas: 2,
			wantErr:      false,
		},
		{
			name: "zero replicas",
			alloc: infernoConfig.AllocationData{
				NumReplicas: 0,
				MaxBatch:    8,
				Load: infernoConfig.ServerLoadSpec{
					ArrivalRate:  120.0,
					AvgInTokens:  100,
					AvgOutTokens: 200,
				},
				TTFTAverage: 150.0,
				ITLAverage:  25.0,
			},
			wantLambda:   0.0,
			wantReplicas: 0,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, err := convertAllocToEnvironment(tt.alloc)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertAllocToEnvironment() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				// If we expect an error, env should be nil, so skip field checks
				return
			}
			if env.Lambda != tt.wantLambda {
				t.Errorf("Lambda = %v, want %v", env.Lambda, tt.wantLambda)
			}
			if env.NumReplicas != tt.wantReplicas {
				t.Errorf("NumReplicas = %v, want %v", env.NumReplicas, tt.wantReplicas)
			}
		})
	}
}

// TestGetFactoredState tests state multiplication by a factor
func TestGetFactoredState(t *testing.T) {
	tests := []struct {
		name       string
		state      []float64
		factor     float64
		wantResult []float64
	}{
		{
			name:       "multiply by 0.5",
			state:      []float64{5.0, 2.5, 10.0, 0.15},
			factor:     0.5,
			wantResult: []float64{2.5, 1.25, 5.0, 0.075},
		},
		{
			name:       "multiply by 2.0",
			state:      []float64{1.0, 2.0, 3.0, 4.0},
			factor:     2.0,
			wantResult: []float64{2.0, 4.0, 6.0, 8.0},
		},
		{
			name:       "multiply by 1.0",
			state:      []float64{1.0, 2.0, 3.0, 4.0},
			factor:     1.0,
			wantResult: []float64{1.0, 2.0, 3.0, 4.0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFactoredState(tt.state, tt.factor)
			if len(result) != len(tt.wantResult) {
				t.Errorf("result length = %d, want %d", len(result), len(tt.wantResult))
				return
			}
			for i := range result {
				if result[i] != tt.wantResult[i] {
					t.Errorf("result[%d] = %v, want %v", i, result[i], tt.wantResult[i])
				}
			}
		})
	}
}

// TestFindServerInSystemData tests server lookup in SystemData
func TestFindServerInSystemData(t *testing.T) {
	systemData := &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			Servers: infernoConfig.ServerData{
				Spec: []infernoConfig.ServerSpec{
					{Name: "server1"},
					{Name: "server2"},
				},
			},
		},
	}

	tests := []struct {
		name       string
		serverName string
		wantFound  bool
	}{
		{
			name:       "existing server",
			serverName: "server2",
			wantFound:  true,
		},
		{
			name:       "non-existent server",
			serverName: "nonexistent",
			wantFound:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := findServerInSystemData(systemData, tt.serverName)
			if (server != nil) != tt.wantFound {
				t.Errorf("findServerInSystemData() found = %v, want %v", server != nil, tt.wantFound)
			}
		})
	}
}

// TestFindSLOInSystemData tests SLO lookup for model-class pairs
func TestFindSLOInSystemData(t *testing.T) {
	systemData := &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			ServiceClasses: infernoConfig.ServiceClassData{
				Spec: []infernoConfig.ServiceClassSpec{
					{
						Name:     "premium",
						Priority: 1,
						ModelTargets: []infernoConfig.ModelTarget{
							{
								Model:    "llama-3-8b",
								SLO_TTFT: 100.0,
								SLO_ITL:  50.0,
							},
							{
								Model:    "llama-3-70b",
								SLO_TTFT: 200.0,
								SLO_ITL:  75.0,
							},
						},
					},
					{
						Name:     "standard",
						Priority: 2,
						ModelTargets: []infernoConfig.ModelTarget{
							{
								Model:    "llama-3-8b",
								SLO_TTFT: 150.0,
								SLO_ITL:  80.0,
							},
						},
					},
					{
						Name:     "invalid-slos",
						Priority: 3,
						ModelTargets: []infernoConfig.ModelTarget{
							{
								Model:    "bad-model",
								SLO_TTFT: -100.0, // Invalid: negative
								SLO_ITL:  50.0,
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name      string
		modelName string
		className string
		wantErr   bool
		wantTTFT  float64
		wantITL   float64
	}{
		{
			name:      "existing model and class",
			modelName: "llama-3-8b",
			className: "premium",
			wantErr:   false,
			wantTTFT:  100.0,
			wantITL:   50.0,
		},
		{
			name:      "different model same class",
			modelName: "llama-3-70b",
			className: "premium",
			wantErr:   false,
			wantTTFT:  200.0,
			wantITL:   75.0,
		},
		{
			name:      "same model different class",
			modelName: "llama-3-8b",
			className: "standard",
			wantErr:   false,
			wantTTFT:  150.0,
			wantITL:   80.0,
		},
		{
			name:      "non-existent class",
			modelName: "llama-3-8b",
			className: "nonexistent",
			wantErr:   true,
		},
		{
			name:      "non-existent model in valid class",
			modelName: "nonexistent-model",
			className: "premium",
			wantErr:   true,
		},
		{
			name:      "invalid SLO values",
			modelName: "bad-model",
			className: "invalid-slos",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slos, err := findSLOInSystemData(systemData, tt.modelName, tt.className)
			if (err != nil) != tt.wantErr {
				t.Errorf("findSLOInSystemData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(slos) != 2 {
					t.Errorf("findSLOInSystemData() returned %d SLOs, want 2", len(slos))
					return
				}
				if slos[0] != tt.wantTTFT {
					t.Errorf("TTFT = %v, want %v", slos[0], tt.wantTTFT)
				}
				if slos[1] != tt.wantITL {
					t.Errorf("ITL = %v, want %v", slos[1], tt.wantITL)
				}
			}
		})
	}
}

// TestUpdateModelPerfDataInSystemData tests SystemData performance updates
func TestUpdateModelPerfDataInSystemData(t *testing.T) {
	tests := []struct {
		name       string
		systemData *infernoConfig.SystemData
		modelName  string
		accName    string
		tunedAlpha float64
		tunedBeta  float64
		tunedGamma float64
		tunedDelta float64
		wantErr    bool
	}{
		{
			name: "valid update",
			systemData: &infernoConfig.SystemData{
				Spec: infernoConfig.SystemSpec{
					Models: infernoConfig.ModelData{
						PerfData: []infernoConfig.ModelAcceleratorPerfData{
							{
								Name:     "llama-3-8b",
								Acc:      "A100",
								AccCount: 1,
								DecodeParms: infernoConfig.DecodeParms{
									Alpha: 5.0,
									Beta:  2.5,
								},
								PrefillParms: infernoConfig.PrefillParms{
									Gamma: 10.0,
									Delta: 0.15,
								},
							},
						},
					},
				},
			},
			modelName:  "llama-3-8b",
			accName:    "A100",
			tunedAlpha: 5.5,
			tunedBeta:  2.8,
			tunedGamma: 11.0,
			tunedDelta: 0.16,
			wantErr:    false,
		},
		{
			name: "multiple models - update specific one",
			systemData: &infernoConfig.SystemData{
				Spec: infernoConfig.SystemSpec{
					Models: infernoConfig.ModelData{
						PerfData: []infernoConfig.ModelAcceleratorPerfData{
							{
								Name:     "llama-3-8b",
								Acc:      "A100",
								AccCount: 1,
								DecodeParms: infernoConfig.DecodeParms{
									Alpha: 5.0,
									Beta:  2.5,
								},
								PrefillParms: infernoConfig.PrefillParms{
									Gamma: 10.0,
									Delta: 0.15,
								},
							},
							{
								Name:     "llama-3-70b",
								Acc:      "H100",
								AccCount: 2,
								DecodeParms: infernoConfig.DecodeParms{
									Alpha: 8.0,
									Beta:  3.0,
								},
								PrefillParms: infernoConfig.PrefillParms{
									Gamma: 15.0,
									Delta: 0.2,
								},
							},
						},
					},
				},
			},
			modelName:  "llama-3-70b",
			accName:    "H100",
			tunedAlpha: 8.5,
			tunedBeta:  3.2,
			tunedGamma: 16.0,
			tunedDelta: 0.22,
			wantErr:    false,
		},
		{
			name: "non-existent model",
			systemData: &infernoConfig.SystemData{
				Spec: infernoConfig.SystemSpec{
					Models: infernoConfig.ModelData{
						PerfData: []infernoConfig.ModelAcceleratorPerfData{
							{
								Name:     "llama-3-8b",
								Acc:      "A100",
								AccCount: 1,
								DecodeParms: infernoConfig.DecodeParms{
									Alpha: 5.0,
									Beta:  2.5,
								},
								PrefillParms: infernoConfig.PrefillParms{
									Gamma: 10.0,
									Delta: 0.15,
								},
							},
						},
					},
				},
			},
			modelName:  "nonexistent",
			accName:    "A100",
			tunedAlpha: 5.5,
			tunedBeta:  2.8,
			tunedGamma: 11.0,
			tunedDelta: 0.16,
			wantErr:    true,
		},
		{
			name: "wrong accelerator",
			systemData: &infernoConfig.SystemData{
				Spec: infernoConfig.SystemSpec{
					Models: infernoConfig.ModelData{
						PerfData: []infernoConfig.ModelAcceleratorPerfData{
							{
								Name:     "llama-3-8b",
								Acc:      "A100",
								AccCount: 1,
								DecodeParms: infernoConfig.DecodeParms{
									Alpha: 5.0,
									Beta:  2.5,
								},
								PrefillParms: infernoConfig.PrefillParms{
									Gamma: 10.0,
									Delta: 0.15,
								},
							},
						},
					},
				},
			},
			modelName:  "llama-3-8b",
			accName:    "H100",
			tunedAlpha: 5.5,
			tunedBeta:  2.8,
			tunedGamma: 11.0,
			tunedDelta: 0.16,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunedResults := &tune.TunedResults{
				ServiceParms: &analyzer.ServiceParms{
					Decode: &analyzer.DecodeParms{
						Alpha: float32(tt.tunedAlpha),
						Beta:  float32(tt.tunedBeta),
					},
					Prefill: &analyzer.PrefillParms{
						Gamma: float32(tt.tunedGamma),
						Delta: float32(tt.tunedDelta),
					},
				},
				NIS: 2.5,
			}

			err := updateModelPerfDataInSystemData(tt.systemData, tt.modelName, tt.accName, tunedResults)
			if (err != nil) != tt.wantErr {
				t.Errorf("updateModelPerfDataInSystemData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify the update was applied
				var found bool
				for _, perfData := range tt.systemData.Spec.Models.PerfData {
					if perfData.Name == tt.modelName && perfData.Acc == tt.accName {
						found = true
						if perfData.DecodeParms.Alpha != float32(tt.tunedAlpha) {
							t.Errorf("Alpha = %v, want %v", perfData.DecodeParms.Alpha, tt.tunedAlpha)
						}
						if perfData.DecodeParms.Beta != float32(tt.tunedBeta) {
							t.Errorf("Beta = %v, want %v", perfData.DecodeParms.Beta, tt.tunedBeta)
						}
						if perfData.PrefillParms.Gamma != float32(tt.tunedGamma) {
							t.Errorf("Gamma = %v, want %v", perfData.PrefillParms.Gamma, tt.tunedGamma)
						}
						if perfData.PrefillParms.Delta != float32(tt.tunedDelta) {
							t.Errorf("Delta = %v, want %v", perfData.PrefillParms.Delta, tt.tunedDelta)
						}
					}
				}
				if !found {
					t.Errorf("Model %s with accelerator %s not found after update", tt.modelName, tt.accName)
				}
			}
		})
	}
}

// TestUpdateVAStatusWithTunedParams tests VA status updates with tuned parameters
func TestUpdateVAStatusWithTunedParams(t *testing.T) {
	tests := []struct {
		name        string
		va          *llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		model       string
		accelerator string
		tunedAlpha  float64
		tunedBeta   float64
		tunedGamma  float64
		tunedDelta  float64
		tunedNIS    float64
		wantErr     bool
	}{
		{
			name: "valid update with new tuned params",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-va",
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ModelID: "llama-3-8b",
				},
			},
			model:       "llama-3-8b",
			accelerator: "A100",
			tunedAlpha:  5.5,
			tunedBeta:   2.8,
			tunedGamma:  11.0,
			tunedDelta:  0.16,
			tunedNIS:    2.5,
			wantErr:     false,
		},
		{
			name: "update existing tuned params",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-va-existing",
					Namespace: "default",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ModelID: "llama-3-8b",
				},
				Status: llmdVariantAutoscalingV1alpha1.VariantAutoscalingStatus{
					TunerPerfData: &llmdVariantAutoscalingV1alpha1.TunerPerfData{
						Model:       "llama-3-8b",
						Accelerator: "A100",
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms: map[string]string{
								"alpha": "5.0",
								"beta":  "2.5",
							},
							PrefillParms: map[string]string{
								"gamma": "10.0",
								"delta": "0.15",
							},
						},
					},
				},
			},
			model:       "llama-3-8b",
			accelerator: "A100",
			tunedAlpha:  6.0,
			tunedBeta:   3.0,
			tunedGamma:  12.0,
			tunedDelta:  0.18,
			tunedNIS:    3.0,
			wantErr:     false,
		},
		{
			name: "update with different accelerator",
			va: &llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-va-h100",
					Namespace: "gpu-namespace",
				},
				Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
					ModelID: "llama-3-70b",
				},
			},
			model:       "llama-3-70b",
			accelerator: "H100",
			tunedAlpha:  8.5,
			tunedBeta:   3.5,
			tunedGamma:  16.0,
			tunedDelta:  0.22,
			tunedNIS:    1.8,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tunedResults := &tune.TunedResults{
				ServiceParms: &analyzer.ServiceParms{
					Decode: &analyzer.DecodeParms{
						Alpha: float32(tt.tunedAlpha),
						Beta:  float32(tt.tunedBeta),
					},
					Prefill: &analyzer.PrefillParms{
						Gamma: float32(tt.tunedGamma),
						Delta: float32(tt.tunedDelta),
					},
				},
				NIS: tt.tunedNIS,
				Covariance: mat.NewDense(4, 4, []float64{
					0.1, 0, 0, 0,
					0, 0.1, 0, 0,
					0, 0, 0.1, 0,
					0, 0, 0, 0.1,
				}),
			}

			err := updateVAStatusWithTunedParams(tt.va, tt.model, tt.accelerator, tunedResults)
			if (err != nil) != tt.wantErr {
				t.Errorf("updateVAStatusWithTunedParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Verify model and accelerator
				if tt.va.Status.TunerPerfData.Model != tt.model {
					t.Errorf("Model = %v, want %v", tt.va.Status.TunerPerfData.Model, tt.model)
				}
				if tt.va.Status.TunerPerfData.Accelerator != tt.accelerator {
					t.Errorf("Accelerator = %v, want %v", tt.va.Status.TunerPerfData.Accelerator, tt.accelerator)
				}

				// Verify decode parameters
				alphaStr := tt.va.Status.TunerPerfData.PerfParms.DecodeParms["alpha"]
				alpha, err := strconv.ParseFloat(alphaStr, 64)
				if err != nil {
					t.Errorf("Failed to parse alpha: %v", err)
				}
				if math.Abs(alpha-tt.tunedAlpha) > epsilon {
					t.Errorf("Alpha = %v, want %v", alpha, tt.tunedAlpha)
				}

				betaStr := tt.va.Status.TunerPerfData.PerfParms.DecodeParms["beta"]
				beta, err := strconv.ParseFloat(betaStr, 64)
				if err != nil {
					t.Errorf("Failed to parse beta: %v", err)
				}
				if math.Abs(beta-tt.tunedBeta) > epsilon {
					t.Errorf("Beta = %v, want %v", beta, tt.tunedBeta)
				}

				// Verify prefill parameters
				gammaStr := tt.va.Status.TunerPerfData.PerfParms.PrefillParms["gamma"]
				gamma, err := strconv.ParseFloat(gammaStr, 64)
				if err != nil {
					t.Errorf("Failed to parse gamma: %v", err)
				}
				if math.Abs(gamma-tt.tunedGamma) > epsilon {
					t.Errorf("Gamma = %v, want %v", gamma, tt.tunedGamma)
				}

				deltaStr := tt.va.Status.TunerPerfData.PerfParms.PrefillParms["delta"]
				delta, err := strconv.ParseFloat(deltaStr, 64)
				if err != nil {
					t.Errorf("Failed to parse delta: %v", err)
				}
				if math.Abs(delta-tt.tunedDelta) > epsilon {
					t.Errorf("Delta = %v, want %v", delta, tt.tunedDelta)
				}

				// Verify NIS
				nisStr := tt.va.Status.TunerPerfData.NIS
				nis, err := strconv.ParseFloat(nisStr, 64)
				if err != nil {
					t.Errorf("Failed to parse NIS: %v", err)
				}
				if math.Abs(nis-tt.tunedNIS) > epsilon {
					t.Errorf("NIS = %v, want %v", nis, tt.tunedNIS)
				}

				// Verify covariance matrix exists
				if len(tt.va.Status.TunerPerfData.CovarianceMatrix) != 4 {
					t.Errorf("CovarianceMatrix rows = %v, want 4", len(tt.va.Status.TunerPerfData.CovarianceMatrix))
				}

				// Verify UpdatedAt is set
				if tt.va.Status.TunerPerfData.UpdatedAt.IsZero() {
					t.Error("UpdatedAt timestamp is zero")
				}
			}
		})
	}
}
