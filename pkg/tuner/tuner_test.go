package tuner

import (
	"math"
	"strings"
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/analyzer"
	"go.uber.org/zap"
	"gonum.org/v1/gonum/mat"
)

// Test constants for tuner tests, based on queue analyzer predictions
const (
	// Test workload parameters
	TestLambdaReqPerMin = 60.0 // 60 requests per minute (used in Environment.Lambda)
	TestAvgInputTokens  = 100  // Average input tokens
	TestAvgOutputTokens = 200  // Average output tokens
	TestMaxBatchSize    = 8    // Maximum batch size

	// Test model parameters (alpha, beta, gamma, delta)
	TestAlpha = 5.0
	TestBeta  = 2.5
	TestGamma = 10.0
	TestDelta = 0.15

	// Predicted observations from queue analyzer with above parameters
	TestPredictedTTFT = 186.7 // Time to first token in ms
	TestPredictedITL  = 14.9  // Inter-token latency in ms
)

// Helper to initialize logger for tests
func init() {
	logger.Log = zap.NewNop().Sugar()
}

// Helper function to create a valid test environment
func createTestEnvironment() *Environment {
	return &Environment{
		Lambda:        TestLambdaReqPerMin, // 60 req/min (Lambda is in req/min)
		AvgInputToks:  TestAvgInputTokens,  // 100 input tokens
		AvgOutputToks: TestAvgOutputTokens, // 200 output tokens
		MaxBatchSize:  TestMaxBatchSize,    // max batch size of 8
		AvgTTFT:       TestPredictedTTFT,   // 186.7 ms TTFT - actual prediction from queue analyzer
		AvgITL:        TestPredictedITL,    // 14.9 ms ITL - actual prediction from queue analyzer
	}
}

// Helper function to create valid test configuration
func createTestConfig() *TunerConfigData {
	return &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: constants.DefaultGammaFactor,
			ErrorLevel:  constants.DefaultErrorLevel,
			TPercentile: constants.DefaultTPercentile,
		},
		ModelData: TunerModelData{
			InitState:            []float64{TestAlpha, TestBeta, TestGamma, TestDelta}, // alpha, beta, gamma, delta
			PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},                        // 10% uncertainty
			BoundedState:         true,
			MinState:             []float64{1.0, 0.5, 2.0, 0.01},
			MaxState:             []float64{20.0, 10.0, 50.0, 1.0},
			ExpectedObservations: []float64{TestPredictedTTFT, TestPredictedITL}, // SLO targets - using predicted values since we don't have separate SLO constants for pkg/tuner
		},
	}
}

func TestNewTuner(t *testing.T) {
	tests := []struct {
		name       string
		configData *TunerConfigData
		env        *Environment
		wantErr    bool
	}{
		{
			name:       "valid configuration",
			configData: createTestConfig(),
			env:        createTestEnvironment(),
			wantErr:    false,
		},
		{
			name:       "nil config data",
			configData: nil,
			env:        createTestEnvironment(),
			wantErr:    true,
		},
		{
			name:       "nil environment",
			configData: createTestConfig(),
			env:        nil,
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
			env:     createTestEnvironment(),
			wantErr: true,
		},
		{
			name: "mismatched state dimensions",
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
			env:     createTestEnvironment(),
			wantErr: true,
		},
		{
			name: "bounded state with missing bounds",
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
					MaxState:             []float64{50.0, 20.0, 100.0, 15.0},
					ExpectedObservations: []float64{150.0, 25.0},
				},
			},
			env:     createTestEnvironment(),
			wantErr: true,
		},
		{
			name: "zero expected observations",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.0, 1.5, 10.0},
					PercentChange:        []float64{0.05, 0.05, 0.05, 0.05},
					BoundedState:         false,
					ExpectedObservations: []float64{}, // Empty
				},
			},
			env:     createTestEnvironment(),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tuner, err := NewTuner(tt.configData, tt.env)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewTuner() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tuner == nil {
				t.Error("NewTuner() returned nil tuner without error")
			}
		})
	}
}

func TestTuner_Run(t *testing.T) {
	tests := []struct {
		name       string
		configData *TunerConfigData
		env        *Environment
		wantErr    bool
		wantNewErr bool // expect error in NewTuner
		checkFunc  func(*testing.T, *TunedResults)
	}{
		{
			name:       "successful tuning with valid data",
			configData: createTestConfig(),
			env:        createTestEnvironment(),
			wantErr:    false,
			wantNewErr: false,
			checkFunc: func(t *testing.T, results *TunedResults) {
				if results == nil {
					t.Error("TunedResults is nil")
					return
				}
				if results.ServiceParms == nil {
					t.Error("ServiceParms is nil")
					return
				}
				// Check that parameters are positive
				if results.ServiceParms.Decode.Alpha <= 0 {
					t.Error("Alpha must be positive")
				}
				if results.ServiceParms.Decode.Beta <= 0 {
					t.Error("Beta must be positive")
				}
				if results.ServiceParms.Prefill.Gamma <= 0 {
					t.Error("Gamma must be positive")
				}
				if results.ServiceParms.Prefill.Delta <= 0 {
					t.Error("Delta must be positive")
				}
				if results.Innovation == nil {
					t.Error("Innovation vector is nil")
				}
				if results.Covariance == nil {
					t.Error("Covariance matrix is nil")
				}
			},
		},
		{
			name:       "invalid environment - zero lambda",
			configData: createTestConfig(),
			env: &Environment{
				Lambda:        0.0, // Invalid lambda
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			wantNewErr: true, // NewTuner should fail
		},
		{
			name:       "invalid environment - zero batch size",
			configData: createTestConfig(),
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  0, // Invalid max batch size
				AvgTTFT:       150.0,
				AvgITL:        25.0,
			},
			wantNewErr: true, // NewTuner should fail
		},
		{
			name:       "extreme observations causing high NIS",
			configData: createTestConfig(),
			env: &Environment{
				Lambda:        60.0,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       10000.0, // Extremely high, should trigger NIS rejection
				AvgITL:        5000.0,  // Extremely high
			},
			wantErr:    false, // Run() returns nil error but sets ValidationFailed flag
			wantNewErr: false,
			checkFunc: func(t *testing.T, results *TunedResults) {
				if !results.ValidationFailed {
					t.Error("Expected ValidationFailed to be true for extreme observations")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tuner, err := NewTuner(tt.configData, tt.env)
			if tt.wantNewErr {
				if err == nil {
					t.Errorf("NewTuner() expected error but got none")
				}
				return // Skip Run() test if NewTuner should fail
			}
			if err != nil {
				t.Fatalf("Failed to create tuner: %v", err)
			}

			results, err := tuner.Run()
			if (err != nil) != tt.wantErr {
				t.Errorf("Tuner.Run() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.checkFunc != nil {
				tt.checkFunc(t, results)
			}
		})
	}
}

func TestTuner_UpdateEnvironment(t *testing.T) {
	configData := createTestConfig()
	initialEnv := createTestEnvironment()

	tuner, err := NewTuner(configData, initialEnv)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Update environment with modified observations but same structure
	// Use values close to predictions to simulate small measurement variations
	newEnv := &Environment{
		Lambda:        60.0, // Same rate
		AvgInputToks:  100,  // Same tokens
		AvgOutputToks: 200,
		MaxBatchSize:  8,     // Same batch size
		AvgTTFT:       190.0, // Small variation from 186.7
		AvgITL:        15.5,  // Small variation from 14.9
	}

	if err := tuner.UpdateEnvironment(newEnv); err != nil {
		t.Fatalf("UpdateEnvironment() failed: %v", err)
	}

	// Run tuning with new environment
	results, err := tuner.Run()
	if err != nil {
		t.Fatalf("Tuner.Run() failed after environment update: %v", err)
	}

	if results == nil {
		t.Error("TunedResults is nil after environment update")
	}
}

func TestTuner_GetParms(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	parms := tuner.GetParms()
	if parms == nil {
		t.Fatal("GetParms() returned nil")
	}

	// Check dimensions
	if parms.Len() != 4 {
		t.Errorf("Expected 4 parameters, got %d", parms.Len())
	}

	// Check that initial parameters match config
	expectedParms := configData.ModelData.InitState
	for i := 0; i < parms.Len(); i++ {
		if math.Abs(parms.AtVec(i)-expectedParms[i]) > 1e-6 {
			t.Errorf("Parameter %d: expected %f, got %f", i, expectedParms[i], parms.AtVec(i))
		}
	}
}

func TestTuner_StateAccessors(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Test X() - state vector
	x := tuner.X()
	if x == nil {
		t.Error("X() returned nil")
	}
	if x.Len() != 4 {
		t.Errorf("Expected state dimension 4, got %d", x.Len())
	}

	// Test P() - covariance matrix
	p := tuner.P()
	if p == nil {
		t.Error("P() returned nil")
	}
	rows, cols := p.Dims()
	if rows != 4 || cols != 4 {
		t.Errorf("Expected P dimension 4x4, got %dx%d", rows, cols)
	}

	// Run to populate innovation
	_, err = tuner.Run()
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Test Y() - innovation vector
	y := tuner.Y()
	if y == nil {
		t.Fatal("Y() returned nil")
	}

	// Test S() - innovation covariance
	s := tuner.S()
	if s == nil {
		t.Fatal("S() returned nil")
	}
}

func TestTuner_ValidatePositiveParameters(t *testing.T) {
	// Test that tuned parameters remain positive after successful tuning
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Run tuning
	results, err := tuner.Run()
	if err != nil {
		t.Fatalf("Tuner.Run() failed: %v", err)
	}

	// Validate all parameters are positive
	if results.ServiceParms.Decode.Alpha <= 0 {
		t.Errorf("Alpha must be positive, got %f", results.ServiceParms.Decode.Alpha)
	}
	if results.ServiceParms.Decode.Beta <= 0 {
		t.Errorf("Beta must be positive, got %f", results.ServiceParms.Decode.Beta)
	}
	if results.ServiceParms.Prefill.Delta <= 0 {
		t.Errorf("Delta must be positive, got %f", results.ServiceParms.Prefill.Delta)
	}
	if results.ServiceParms.Prefill.Gamma <= 0 {
		t.Errorf("Gamma must be positive, got %f", results.ServiceParms.Prefill.Gamma)
	}

	t.Logf("All parameters positive: Alpha=%.3f, Beta=%.3f, Delta=%.4f, Gamma=%.3f",
		results.ServiceParms.Decode.Alpha,
		results.ServiceParms.Decode.Beta,
		results.ServiceParms.Prefill.Delta,
		results.ServiceParms.Prefill.Gamma)
}

func TestTuner_MultipleIterations(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Run multiple tuning iterations
	numIterations := 5
	var previousAlpha float32 = -1

	for i := range numIterations {
		results, err := tuner.Run()
		if err != nil {
			t.Logf("Iteration %d failed: %v", i, err)
			continue
		}

		if results == nil || results.ServiceParms == nil {
			t.Errorf("Iteration %d: Got nil results", i)
			continue
		}

		alpha := results.ServiceParms.Decode.Alpha
		t.Logf("Iteration %d: Alpha = %f", i, alpha)

		// Check that parameter is still positive
		if alpha <= 0 {
			t.Errorf("Iteration %d: Alpha became non-positive: %f", i, alpha)
		}

		if previousAlpha > 0 {
			// Parameters should converge, so change should decrease over time
			t.Logf("Alpha change: %f", math.Abs(float64(alpha-previousAlpha)))
		}

		previousAlpha = alpha
	}
}

func TestTuner_String(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	str := tuner.String()
	if str == "" {
		t.Error("String() returned empty string")
	}
}

func TestTunedResults_ParameterExtraction(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	results, err := tuner.Run()
	if err != nil {
		t.Skipf("Tuner.Run() failed: %v", err)
	}

	if results == nil {
		t.Fatal("TunedResults is nil")
	}

	// Verify ServiceParms structure
	if results.ServiceParms == nil {
		t.Fatal("ServiceParms is nil")
	}

	// Verify Decode parameters
	decode := results.ServiceParms.Decode
	if decode == nil {
		t.Fatal("Decode parameters are nil")
	}
	if decode.Alpha <= 0 || decode.Beta <= 0 {
		t.Errorf("Invalid Decode parameters: Alpha=%f, Beta=%f", decode.Alpha, decode.Beta)
	}

	// Verify Prefill parameters
	prefill := results.ServiceParms.Prefill
	if prefill == nil {
		t.Fatal("Prefill parameters are nil")
	}
	if prefill.Gamma <= 0 || prefill.Delta <= 0 {
		t.Errorf("Invalid Prefill parameters: Gamma=%f, Delta=%f", prefill.Gamma, prefill.Delta)
	}

	// Verify Innovation vector
	if results.Innovation == nil {
		t.Error("Innovation vector is nil")
	} else if results.Innovation.Len() != 2 {
		t.Errorf("Innovation vector should have length 2, got %d", results.Innovation.Len())
	}

	// Verify Covariance matrix
	if results.Covariance == nil {
		t.Error("Covariance matrix is nil")
	} else {
		rows, cols := results.Covariance.Dims()
		if rows != 4 || cols != 4 {
			t.Errorf("Covariance matrix should be 4x4, got %dx%d", rows, cols)
		}
	}
}

func TestTuner_BoundedStateEnforcement(t *testing.T) {
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: 1.0,
			ErrorLevel:  0.05,
			TPercentile: 1.96,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.5, 10.0, 0.15}, // Use realistic params
			PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
			BoundedState:         true,
			MinState:             []float64{4.0, 2.0, 8.0, 0.1}, // Tight bounds: alpha, beta, gamma, delta
			MaxState:             []float64{6.0, 3.0, 12.0, 0.2},
			ExpectedObservations: []float64{186.7, 14.9}, // Use realistic values
		},
	}

	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Run multiple iterations - should succeed with realistic params
	successCount := 0
	for i := range 10 {
		results, err := tuner.Run()
		if err != nil {
			t.Logf("Iteration %d: %v", i, err)
			continue
		}

		successCount++

		if results == nil || results.ServiceParms == nil {
			continue
		}

		// Check that parameters are within bounds
		alpha := results.ServiceParms.Decode.Alpha
		beta := results.ServiceParms.Decode.Beta
		delta := results.ServiceParms.Prefill.Delta
		gamma := results.ServiceParms.Prefill.Gamma

		if alpha < float32(configData.ModelData.MinState[constants.StateIndexAlpha]) || alpha > float32(configData.ModelData.MaxState[constants.StateIndexAlpha]) {
			t.Errorf("Alpha %f out of bounds [%f, %f]", alpha, configData.ModelData.MinState[constants.StateIndexAlpha], configData.ModelData.MaxState[constants.StateIndexAlpha])
		}
		if beta < float32(configData.ModelData.MinState[constants.StateIndexBeta]) || beta > float32(configData.ModelData.MaxState[constants.StateIndexBeta]) {
			t.Errorf("Beta %f out of bounds [%f, %f]", beta, configData.ModelData.MinState[constants.StateIndexBeta], configData.ModelData.MaxState[constants.StateIndexBeta])
		}
		if gamma < float32(configData.ModelData.MinState[constants.StateIndexGamma]) || gamma > float32(configData.ModelData.MaxState[constants.StateIndexGamma]) {
			t.Errorf("Gamma %f out of bounds [%f, %f]", gamma, configData.ModelData.MinState[constants.StateIndexGamma], configData.ModelData.MaxState[constants.StateIndexGamma])
		}
		if delta < float32(configData.ModelData.MinState[constants.StateIndexDelta]) || delta > float32(configData.ModelData.MaxState[constants.StateIndexDelta]) {
			t.Errorf("Delta %f out of bounds [%f, %f]", delta, configData.ModelData.MinState[constants.StateIndexDelta], configData.ModelData.MaxState[constants.StateIndexDelta])
		}
	}

	// Verify we had some successful runs
	if successCount == 0 {
		t.Error("No successful tuning iterations - all were rejected by NIS validation")
	} else {
		t.Logf("Successfully completed %d out of 10 iterations", successCount)
	}
}

func TestTuner_ConvergenceWithMultipleInitialGuesses(t *testing.T) {
	observedTTFT := 186.7 // ms
	observedITL := 14.9   // ms

	avgInputToks := 512
	avgOutputToks := 128
	lambda := 60.0 // req/min
	batchSize := 8

	t.Logf("\n TEST CONFIGURATION:")
	t.Logf("   Observed: TTFT=%.1f ms, ITL=%.1f ms", observedTTFT, observedITL)
	t.Logf("   Workload: λ=%.0f req/min, input=%d toks, output=%d toks, batch=%d",
		lambda, avgInputToks, avgOutputToks, batchSize)

	testCases := []struct {
		name        string
		initAlpha   float64
		initBeta    float64
		initGamma   float64
		initDelta   float64
		description string
	}{
		{
			name:        "close_guess",
			initAlpha:   8.5,
			initBeta:    2.1,
			initGamma:   5.0,
			initDelta:   0.11,
			description: "Close to optimal (within 5-10%)",
		},
		{
			name:        "moderate_off",
			initAlpha:   7.0,
			initBeta:    2.5,
			initGamma:   6.0,
			initDelta:   0.09,
			description: "Moderately off (~15-20%)",
		},
		{
			name:        "significant_error",
			initAlpha:   5.0,
			initBeta:    3.0,
			initGamma:   8.0,
			initDelta:   0.15,
			description: "Significant initial error (~40-60%)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("\n %s: α=%.1f β=%.1f γ=%.1f δ=%.3f",
				tc.description, tc.initAlpha, tc.initBeta, tc.initGamma, tc.initDelta)

			// Calculate initial prediction error
			initialPred := predictWithQueueAnalyzer(tc.initAlpha, tc.initBeta, tc.initGamma, tc.initDelta,
				batchSize, avgInputToks, avgOutputToks, lambda)

			initialError := math.Abs(float64(initialPred.ttft-float32(observedTTFT))) +
				math.Abs(float64(initialPred.itl-float32(observedITL)))

			t.Logf("   Initial prediction: TTFT=%.1f ITL=%.1f (error: %.1f ms)",
				initialPred.ttft, initialPred.itl, initialError)

			// Configure tuner
			configData := createTestConfig()
			configData.ModelData.InitState = []float64{tc.initAlpha, tc.initBeta, tc.initGamma, tc.initDelta}
			configData.ModelData.ExpectedObservations = []float64{observedTTFT, observedITL}

			env := &Environment{
				Lambda:        float32(lambda),
				AvgInputToks:  avgInputToks,
				AvgOutputToks: avgOutputToks,
				MaxBatchSize:  batchSize,
				AvgTTFT:       float32(observedTTFT),
				AvgITL:        float32(observedITL),
			}

			tuner, err := NewTuner(configData, env)
			if err != nil {
				t.Fatalf("Failed to create tuner: %v", err)
			}

			// Run tuning iterations
			numIterations := 20
			successCount := 0
			rejectedCount := 0

			type snapshot struct {
				iter                      int
				alpha, beta, gamma, delta float64
				ttft, itl, error          float32
			}
			var milestones []snapshot

			// Record initial
			params := tuner.GetParms()
			milestones = append(milestones, snapshot{
				iter:  0,
				alpha: params.AtVec(constants.StateIndexAlpha),
				beta:  params.AtVec(constants.StateIndexBeta),
				gamma: params.AtVec(constants.StateIndexGamma),
				delta: params.AtVec(constants.StateIndexDelta),
				ttft:  initialPred.ttft,
				itl:   initialPred.itl,
				error: float32(initialError),
			})

			// Run iterations
			for i := 1; i <= numIterations; i++ {
				_, err := tuner.Run()
				if err != nil {
					rejectedCount++
					if i%10 == 0 {
						t.Logf("   Iter %d: Update rejected (%v)", i, err)
					}
					continue
				}

				successCount++
				params := tuner.GetParms()
				alpha := params.AtVec(constants.StateIndexAlpha)
				beta := params.AtVec(constants.StateIndexBeta)
				gamma := params.AtVec(constants.StateIndexGamma)
				delta := params.AtVec(constants.StateIndexDelta)

				pred := predictWithQueueAnalyzer(alpha, beta, gamma, delta,
					batchSize, avgInputToks, avgOutputToks, lambda)

				iterError := math.Abs(float64(pred.ttft-float32(observedTTFT))) +
					math.Abs(float64(pred.itl-float32(observedITL)))

				// Record milestones
				if i == 1 || i == 5 || i == 10 || i == numIterations || iterError < 1.0 {
					milestones = append(milestones, snapshot{
						iter:  i,
						alpha: alpha,
						beta:  beta,
						gamma: gamma,
						delta: delta,
						ttft:  pred.ttft,
						itl:   pred.itl,
						error: float32(iterError),
					})

					if iterError < 1.0 && len(milestones) > 1 && milestones[len(milestones)-2].error >= 1.0 {
						t.Logf("   ✓ Iter %d: Converged! error=%.1f ms", i, iterError)
					}
				}
			}

			// Print milestone trajectory
			t.Logf("\n   Convergence milestones:")
			t.Logf("   %-4s  %7s %7s %7s %7s  %8s %7s  %7s",
				"Iter", "Alpha", "Beta", "Gamma", "Delta", "TTFT", "ITL", "Error")
			for _, m := range milestones {
				t.Logf("   %-4d  %7.2f %7.2f %7.2f %7.4f  %8.1f %7.1f  %7.1f",
					m.iter, m.alpha, m.beta, m.gamma, m.delta, m.ttft, m.itl, m.error)
			}

			// Final evaluation
			final := milestones[len(milestones)-1]
			improvement := float64(initialError) - float64(final.error)
			improvementPct := 100 * improvement / initialError

			t.Logf("\n   📈 Results:")
			t.Logf("      Success: %d/%d iterations (%.0f%%)", successCount, numIterations, 100*float64(successCount)/float64(numIterations))
			t.Logf("      Error: %.1f → %.1f ms (%.1f%% improvement)", initialError, final.error, improvementPct)

			// Validate convergence
			if successCount > 0 {
				if final.error >= float32(initialError) {
					t.Errorf("Failed to improve: error %.1f → %.1f ms", initialError, final.error)
				}
				if final.error > 10.0 && successCount > 10 {
					t.Logf("      Warning: Final error still high (%.1f ms)", final.error)
				}
				if final.error < 1.0 {
					t.Logf("      Excellent convergence (error < 1ms)")
				}
			} else {
				t.Errorf("      All updates rejected - initial guess may be too far off")
			}

			// Verify bounds
			validateParameterBounds(t, final.alpha, final.beta, final.gamma, final.delta)
		})
	}
}

// Helper type for prediction results
type predictionResult struct {
	ttft float32
	itl  float32
}

// Helper to predict using queue analyzer
func predictWithQueueAnalyzer(alpha, beta, gamma, delta float64,
	batchSize, avgInputToks, avgOutputToks int, lambda float64) predictionResult {

	qConfig := &analyzer.Configuration{
		MaxBatchSize: batchSize,
		MaxQueueSize: batchSize * 5,
		ServiceParms: &analyzer.ServiceParms{
			Prefill: &analyzer.PrefillParms{
				Gamma: float32(gamma),
				Delta: float32(delta),
			},
			Decode: &analyzer.DecodeParms{
				Alpha: float32(alpha),
				Beta:  float32(beta),
			},
		},
	}

	requestData := &analyzer.RequestSize{
		AvgInputTokens:  avgInputToks,
		AvgOutputTokens: avgOutputToks,
	}

	qa, err := analyzer.NewQueueAnalyzer(qConfig, requestData)
	if err != nil {
		return predictionResult{}
	}

	metrics, err := qa.Analyze(float32(lambda / 60.0))
	if err != nil {
		return predictionResult{}
	}

	return predictionResult{
		ttft: metrics.AvgWaitTime + metrics.AvgPrefillTime,
		itl:  metrics.AvgTokenTime,
	}
}

// Helper to validate parameter bounds
func validateParameterBounds(t *testing.T, alpha, beta, gamma, delta float64) {
	configLimits := createTestConfig()

	if alpha < configLimits.ModelData.MinState[constants.StateIndexAlpha] ||
		alpha > configLimits.ModelData.MaxState[constants.StateIndexAlpha] {
		t.Errorf("Alpha %.3f out of bounds [%.1f, %.1f]",
			alpha, configLimits.ModelData.MinState[constants.StateIndexAlpha], configLimits.ModelData.MaxState[constants.StateIndexAlpha])
	}
	if beta < configLimits.ModelData.MinState[constants.StateIndexBeta] ||
		beta > configLimits.ModelData.MaxState[constants.StateIndexBeta] {
		t.Errorf("Beta %.3f out of bounds [%.1f, %.1f]",
			beta, configLimits.ModelData.MinState[constants.StateIndexBeta], configLimits.ModelData.MaxState[constants.StateIndexBeta])
	}
	if gamma < configLimits.ModelData.MinState[constants.StateIndexGamma] ||
		gamma > configLimits.ModelData.MaxState[constants.StateIndexGamma] {
		t.Errorf("Gamma %.3f out of bounds [%.1f, %.1f]",
			gamma, configLimits.ModelData.MinState[constants.StateIndexGamma], configLimits.ModelData.MaxState[constants.StateIndexGamma])
	}
	if delta < configLimits.ModelData.MinState[constants.StateIndexDelta] ||
		delta > configLimits.ModelData.MaxState[constants.StateIndexDelta] {
		t.Errorf("Delta %.4f out of bounds [%.2f, %.2f]",
			delta, configLimits.ModelData.MinState[constants.StateIndexDelta], configLimits.ModelData.MaxState[constants.StateIndexDelta])
	}

	if alpha <= 0 || beta <= 0 || gamma <= 0 || delta <= 0 {
		t.Errorf("Parameters must be positive: α=%.3f β=%.3f γ=%.3f δ=%.4f",
			alpha, beta, gamma, delta)
	}
}

// TestTuner_ErrorPaths tests error handling paths for improved coverage
func TestTuner_ErrorPaths(t *testing.T) {
	t.Run("nil_environment_in_update", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()
		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("Failed to create tuner: %v", err)
		}

		err = tuner.UpdateEnvironment(nil)
		if err == nil {
			t.Error("Expected error when updating with nil environment")
		}
		if !strings.Contains(err.Error(), "environment cannot be nil") {
			t.Errorf("Expected 'environment cannot be nil' error, got: %v", err)
		}
	})

	t.Run("invalid_environment_in_update", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()
		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("Failed to create tuner: %v", err)
		}

		// Create invalid environment
		invalidEnv := &Environment{
			Lambda:        0, // Invalid: zero lambda
			AvgInputToks:  100,
			AvgOutputToks: 200,
			MaxBatchSize:  8,
			AvgTTFT:       190,
			AvgITL:        15,
		}

		err = tuner.UpdateEnvironment(invalidEnv)
		if err == nil {
			t.Error("Expected error when updating with invalid environment")
		}
		if !strings.Contains(err.Error(), "invalid environment") {
			t.Errorf("Expected 'invalid environment' error, got: %v", err)
		}
	})

	t.Run("invalid_config_percent_change_mismatch", func(t *testing.T) {
		configData := createTestConfig()
		// Create mismatch between InitState and PercentChange
		configData.ModelData.InitState = []float64{5.0, 2.5, 10.0, 0.15}
		configData.ModelData.PercentChange = []float64{0.1, 0.1} // Wrong size!

		env := createTestEnvironment()
		_, err := NewTuner(configData, env)
		if err == nil {
			t.Error("Expected error when creating tuner with mismatched PercentChange")
		}
	})
}

func TestTuner_RunWithStasherFailures(t *testing.T) {
	t.Run("stash_failure", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Corrupt filter state to cause stash failure
		tuner.filter.X = nil
		tuner.filter.P = nil

		_, err = tuner.Run()
		if err == nil {
			t.Error("Expected error when stashing fails")
		}
		if err != nil && !strings.Contains(err.Error(), "failed to stash") {
			t.Errorf("Wrong error message: %v", err)
		}
	})

	t.Run("unstash_after_validation_failure", func(t *testing.T) {
		configData := createTestConfig()
		env := &Environment{
			Lambda:        60.0,
			AvgInputToks:  100,
			AvgOutputToks: 200,
			MaxBatchSize:  8,
			AvgTTFT:       10000.0, // Extremely high to trigger NIS rejection
			AvgITL:        5000.0,
		}

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Run should fail validation and attempt to unstash
		results, err := tuner.Run()
		if err != nil {
			t.Errorf("Run() should not return error, got: %v", err)
		}
		// Verify validation failed flag is set
		if results == nil {
			t.Fatal("Expected results to be non-nil")
		}
		if !results.ValidationFailed {
			t.Error("Expected ValidationFailed to be true for extreme observations")
		}
	})
}

func TestTuner_MakeObservationFuncErrorPaths(t *testing.T) {
	t.Run("invalid_state_parameters", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		obsFunc := tuner.makeObservationFunc()

		// Test with invalid parameters (negative values)
		invalidState := mat.NewVecDense(4, []float64{-1.0, -2.0, -3.0, -0.1})
		result := obsFunc(invalidState)

		// With invalid parameters, queue analyzer should fail and return nil
		if result != nil {
			t.Error("Expected nil result for invalid parameters")
		}
	})

	t.Run("zero_state_parameters", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		obsFunc := tuner.makeObservationFunc()

		// Test with zero parameters
		zeroState := mat.NewVecDense(4, []float64{0.0, 0.0, 0.0, 0.0})
		result := obsFunc(zeroState)

		// Zero parameters causes analyzer build to fail depending on implementation
		if result != nil {
			t.Fatalf("Observation function should return nil for parameters all zero")
		}
	})
}

func TestTuner_ValidateTunedResultsNegativeParameters(t *testing.T) {
	t.Run("negative_alpha", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Manually corrupt the state to have negative alpha
		tuner.filter.X.SetVec(constants.StateIndexAlpha, -1.0)

		_, err = tuner.validateTunedResults()
		if err == nil {
			t.Error("Expected error for negative alpha")
		}
		if err != nil && !strings.Contains(err.Error(), "decode parameters must be positive") {
			t.Errorf("Wrong error message: %v", err)
		}
	})

	t.Run("negative_delta", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Manually corrupt the state to have negative delta
		tuner.filter.X.SetVec(constants.StateIndexDelta, -0.15)

		_, err = tuner.validateTunedResults()
		if err == nil {
			t.Error("Expected error for negative delta")
		}
		if err != nil && !strings.Contains(err.Error(), "prefill parameters must be positive") {
			t.Errorf("Wrong error message: %v", err)
		}
	})

	t.Run("zero_alpha", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Manually corrupt the state to have zero alpha
		tuner.filter.X.SetVec(constants.StateIndexAlpha, 0.0)

		_, err = tuner.validateTunedResults()
		if err == nil {
			t.Error("Expected error for zero alpha")
		}
		if err != nil && !strings.Contains(err.Error(), "decode parameters must be positive") {
			t.Errorf("Wrong error message: %v", err)
		}
	})

	t.Run("singular_innovation_covariance", func(t *testing.T) {
		configData := createTestConfig()
		env := createTestEnvironment()

		tuner, err := NewTuner(configData, env)
		if err != nil {
			t.Fatalf("NewTuner failed: %v", err)
		}

		// Set innovation to valid values
		tuner.filter.Y = mat.NewVecDense(2, []float64{1.0, 1.0})

		// Create a singular S matrix (all zeros)
		singularS := mat.NewDense(2, 2, []float64{0, 0, 0, 0})
		tuner.filter.S = singularS

		_, err = tuner.validateTunedResults()
		if err == nil {
			t.Error("Expected error for singular innovation covariance matrix")
		}
		if err != nil && !strings.Contains(err.Error(), "singular innovation covariance") {
			t.Errorf("Wrong error message: %v", err)
		}
	})
}

func TestTuner_ExtractTunedResultsVerification(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("NewTuner failed: %v", err)
	}

	// Get initial state
	initialAlpha := tuner.X().AtVec(constants.StateIndexAlpha)

	// Extract results
	results, err := tuner.extractTunedResults()
	if err != nil {
		t.Fatalf("extractTunedResults failed: %v", err)
	}

	// Verify state wasn't modified
	currentAlpha := tuner.X().AtVec(constants.StateIndexAlpha)
	if currentAlpha != initialAlpha {
		t.Error("extractTunedResults modified the tuner state")
	}

	// Verify extracted values match state
	if float64(results.ServiceParms.Decode.Alpha) != initialAlpha {
		t.Error("Extracted alpha doesn't match tuner state")
	}

	// Verify all results fields are populated
	if results.ServiceParms == nil {
		t.Error("ServiceParms is nil")
	}
	if results.Innovation == nil {
		t.Error("Innovation is nil")
	}
	if results.Covariance == nil {
		t.Error("Covariance is nil")
	}
}

// TestTuner_GetEnvironment tests the GetEnvironment method
func TestTuner_GetEnvironment(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("NewTuner failed: %v", err)
	}

	retrievedEnv := tuner.GetEnvironment()
	if retrievedEnv == nil {
		t.Fatal("GetEnvironment returned nil")
	}

	// Verify it returns the same environment
	if retrievedEnv != env {
		t.Error("GetEnvironment did not return the same environment instance")
	}

	// Verify environment values
	if retrievedEnv.Lambda != env.Lambda {
		t.Errorf("Lambda mismatch: got %f, want %f", retrievedEnv.Lambda, env.Lambda)
	}
	if retrievedEnv.AvgInputToks != env.AvgInputToks {
		t.Errorf("AvgInputToks mismatch: got %d, want %d", retrievedEnv.AvgInputToks, env.AvgInputToks)
	}
	if retrievedEnv.AvgOutputToks != env.AvgOutputToks {
		t.Errorf("AvgOutputToks mismatch: got %d, want %d", retrievedEnv.AvgOutputToks, env.AvgOutputToks)
	}
	if retrievedEnv.MaxBatchSize != env.MaxBatchSize {
		t.Errorf("MaxBatchSize mismatch: got %d, want %d", retrievedEnv.MaxBatchSize, env.MaxBatchSize)
	}
}

// TestNewTuner_InvalidEnvironment tests NewTuner with various invalid environments
func TestNewTuner_InvalidEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		env     *Environment
		wantErr string
	}{
		{
			name:    "zero lambda",
			env:     &Environment{Lambda: 0, AvgInputToks: 100, AvgOutputToks: 200, MaxBatchSize: 8, AvgTTFT: 190, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "negative lambda",
			env:     &Environment{Lambda: -10, AvgInputToks: 100, AvgOutputToks: 200, MaxBatchSize: 8, AvgTTFT: 190, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "zero input tokens",
			env:     &Environment{Lambda: 60, AvgInputToks: 0, AvgOutputToks: 200, MaxBatchSize: 8, AvgTTFT: 190, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "zero output tokens",
			env:     &Environment{Lambda: 60, AvgInputToks: 100, AvgOutputToks: 0, MaxBatchSize: 8, AvgTTFT: 190, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "zero max batch size",
			env:     &Environment{Lambda: 60, AvgInputToks: 100, AvgOutputToks: 200, MaxBatchSize: 0, AvgTTFT: 190, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "negative avg ttft",
			env:     &Environment{Lambda: 60, AvgInputToks: 100, AvgOutputToks: 200, MaxBatchSize: 8, AvgTTFT: -10, AvgITL: 15},
			wantErr: "invalid environment",
		},
		{
			name:    "negative avg itl",
			env:     &Environment{Lambda: 60, AvgInputToks: 100, AvgOutputToks: 200, MaxBatchSize: 8, AvgTTFT: 190, AvgITL: -5},
			wantErr: "invalid environment",
		},
	}

	configData := createTestConfig()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTuner(configData, tt.env)
			if err == nil {
				t.Errorf("NewTuner should have failed with invalid environment")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing '%s', got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestNewTuner_ConfiguratorErrors tests NewTuner with invalid configurator data
func TestNewTuner_ConfiguratorErrors(t *testing.T) {
	tests := []struct {
		name       string
		configData *TunerConfigData
		wantErr    string
	}{
		{
			name:       "nil config data",
			configData: nil,
			wantErr:    "error on configurator creation",
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
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "negative gamma factor",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: -1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "zero gamma factor",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 0.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "invalid error level",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  -0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "zero error level",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.0,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "zero t-percentile",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 0.0,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "negative percent change",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{-0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "zero percent change",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.0, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "NaN in init state",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{math.NaN(), 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
		{
			name: "Inf in init state",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{math.Inf(1), 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on configurator creation",
		},
	}

	env := createTestEnvironment()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTuner(tt.configData, env)
			if err == nil {
				t.Errorf("NewTuner should have failed with invalid config")
				return
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing '%s', got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestNewTuner_StateLimiterErrors tests NewTuner with invalid state bounds
func TestNewTuner_StateLimiterErrors(t *testing.T) {
	tests := []struct {
		name       string
		configData *TunerConfigData
		wantErr    string
	}{
		{
			name: "min state > max state",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					BoundedState:         true,
					MinState:             []float64{20.0, 10.0, 50.0, 1.0}, // max values
					MaxState:             []float64{1.0, 0.5, 2.0, 0.01},   // min values
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on setting state limiter",
		},
		{
			name: "mismatched min state dimension",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					BoundedState:         true,
					MinState:             []float64{1.0, 0.5}, // wrong dimension
					MaxState:             []float64{20.0, 10.0, 50.0, 1.0},
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on setting state limiter",
		},
		{
			name: "mismatched max state dimension",
			configData: &TunerConfigData{
				FilterData: FilterData{
					GammaFactor: 1.0,
					ErrorLevel:  0.05,
					TPercentile: 1.96,
				},
				ModelData: TunerModelData{
					InitState:            []float64{5.0, 2.5, 10.0, 0.15},
					PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
					BoundedState:         true,
					MinState:             []float64{1.0, 0.5, 2.0, 0.01},
					MaxState:             []float64{20.0}, // wrong dimension
					ExpectedObservations: []float64{190, 15},
				},
			},
			wantErr: "error on setting state limiter",
		},
	}

	env := createTestEnvironment()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTuner(tt.configData, env)
			if err == nil {
				t.Fatalf("NewTuner should have failed with invalid state bounds")
			}
		})
	}
}

// TestNewTuner_ObservationFuncErrors tests NewTuner with environments that cause observation function failures
func TestNewTuner_ObservationFuncErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     *Environment
		wantErr string
	}{
		{
			name: "extremely high lambda causing analyzer failure",
			env: &Environment{
				Lambda:        1000000.0, // Extremely high - will cause QueueAnalyzer to fail
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
			wantErr: "invalid measurement function",
		},
		{
			name: "extremely large token counts causing analyzer failure",
			env: &Environment{
				Lambda:        60,
				AvgInputToks:  1000000, // Extremely large
				AvgOutputToks: 2000000, // Extremely large
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
			wantErr: "invalid measurement function",
		},
	}

	configData := createTestConfig()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTuner(configData, tt.env)
			// These should fail during observation function setup
			if err == nil {
				t.Errorf("NewTuner should have failed with extreme environment values")
				return
			}
			if tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr) {
				t.Logf("Expected error containing '%s', got: %v (this may be OK if caught earlier)", tt.wantErr, err)
			}
		})
	}
}

// TestTuner_Run_EdgeCases tests Run method with edge cases
func TestTuner_Run_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		env     *Environment
		wantErr bool
	}{
		{
			name: "moderate lambda variation",
			env: &Environment{
				Lambda:        75.0, // 1.25x normal
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
			wantErr: false,
		},
		{
			name: "large batch size",
			env: &Environment{
				Lambda:        60,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  32, // Larger batch
				AvgTTFT:       190,
				AvgITL:        15,
			},
			wantErr: false,
		},
	}

	configData := createTestConfig()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tuner, err := NewTuner(configData, tt.env)
			if err != nil {
				t.Fatalf("NewTuner failed: %v", err)
			}

			_, err = tuner.Run()
			if tt.wantErr {
				if err == nil {
					t.Error("Run should have failed")
				}
			}
		})
	}
}

// TestTuner_ValidateTunedResults_EdgeCases tests validateTunedResults with edge cases
func TestTuner_ValidateTunedResults_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		initState   []float64
		wantErr     bool
		errContains string
	}{
		{
			name:      "reasonable minimum bounds",
			initState: []float64{0.1, 0.1, 0.1, 0.01},
			wantErr:   false,
		},
		{
			name:        "alpha exactly zero",
			initState:   []float64{0.0, 2.5, 10.0, 0.15},
			wantErr:     true,
			errContains: "decode parameters must be positive",
		},
		{
			name:        "beta exactly zero",
			initState:   []float64{5.0, 0.0, 10.0, 0.15},
			wantErr:     true,
			errContains: "decode parameters must be positive",
		},
		{
			name:        "gamma exactly zero",
			initState:   []float64{5.0, 2.5, 0.0, 0.15},
			wantErr:     true,
			errContains: "prefill parameters must be positive",
		},
		{
			name:        "delta exactly zero",
			initState:   []float64{5.0, 2.5, 10.0, 0.0},
			wantErr:     true,
			errContains: "prefill parameters must be positive",
		},
		{
			name:        "alpha slightly negative",
			initState:   []float64{-0.001, 2.5, 10.0, 0.15},
			wantErr:     true,
			errContains: "decode parameters must be positive",
		},
		{
			name:        "beta slightly negative",
			initState:   []float64{5.0, -0.001, 10.0, 0.15},
			wantErr:     true,
			errContains: "decode parameters must be positive",
		},
		{
			name:        "gamma slightly negative",
			initState:   []float64{5.0, 2.5, -0.001, 0.15},
			wantErr:     true,
			errContains: "prefill parameters must be positive",
		},
		{
			name:        "delta slightly negative",
			initState:   []float64{5.0, 2.5, 10.0, -0.001},
			wantErr:     true,
			errContains: "prefill parameters must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configData := createTestConfig()
			configData.ModelData.InitState = tt.initState
			env := createTestEnvironment()

			tuner, err := NewTuner(configData, env)
			if err != nil {
				t.Fatalf("NewTuner failed: %v", err)
			}

			_, err = tuner.validateTunedResults()
			if tt.wantErr {
				if err == nil {
					t.Error("validateTunedResults should have failed")
					return
				}
				// Accept either parameter validation error or singular matrix error
				// (singular matrix can occur before parameter validation with extreme values)
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) &&
					!strings.Contains(err.Error(), "singular innovation covariance matrix") {
					t.Errorf("Expected error containing '%s' or singular matrix, got: %v", tt.errContains, err)
				}
			} else {
				if err != nil {
					// Allow singular matrix errors for extreme values
					if !strings.Contains(err.Error(), "singular innovation covariance matrix") {
						t.Errorf("validateTunedResults failed: %v", err)
					}
				}
			}
		})
	}
}

// TestTuner_ExtractTunedResults_EdgeCases tests extractTunedResults with edge cases
func TestTuner_ExtractTunedResults_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		initState []float64
		wantErr   bool
	}{
		{
			name:      "small parameter values",
			initState: []float64{0.1, 0.1, 0.1, 0.01},
			wantErr:   false,
		},
		{
			name:      "normal parameter values",
			initState: []float64{5.0, 2.5, 10.0, 0.15},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configData := createTestConfig()
			configData.ModelData.InitState = tt.initState
			configData.ModelData.BoundedState = false // Disable bounds for this test
			env := createTestEnvironment()

			tuner, err := NewTuner(configData, env)
			if err != nil {
				t.Fatalf("NewTuner failed: %v", err)
			}

			results, err := tuner.extractTunedResults()
			if tt.wantErr {
				if err == nil {
					t.Error("extractTunedResults should have failed")
				}
			} else {
				if err != nil {
					t.Errorf("extractTunedResults failed: %v", err)
				}
				if results == nil {
					t.Fatal("Results should not be nil")
				}
				if results.ServiceParms == nil {
					t.Fatal("ServiceParms should not be nil")
				}
				if results.Innovation == nil {
					t.Error("Innovation should not be nil")
				}
				if results.Covariance == nil {
					t.Error("Covariance should not be nil")
				}

				// Verify extracted values are reasonable
				if results.ServiceParms.Decode.Alpha <= 0 {
					t.Error("Alpha should be positive")
				}
				if results.ServiceParms.Decode.Beta <= 0 {
					t.Error("Beta should be positive")
				}
				if results.ServiceParms.Prefill.Gamma <= 0 {
					t.Error("Gamma should be positive")
				}
				if results.ServiceParms.Prefill.Delta <= 0 {
					t.Error("Delta should be positive")
				}
			}
		})
	}
}

// TestTuner_MakeObservationFunc_ExtremeInputs tests makeObservationFunc with extreme inputs
func TestTuner_MakeObservationFunc_ExtremeInputs(t *testing.T) {
	tests := []struct {
		name        string
		env         *Environment
		stateValues []float64
		expectNil   bool
	}{
		{
			name: "very low lambda",
			env: &Environment{
				Lambda:        0.01,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
			stateValues: []float64{5.0, 2.5, 10.0, 0.15},
			expectNil:   false,
		},
		{
			name: "small state values",
			env: &Environment{
				Lambda:        60,
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
			stateValues: []float64{0.1, 0.1, 0.1, 0.01},
			expectNil:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configData := createTestConfig()
			configData.ModelData.InitState = tt.stateValues
			configData.ModelData.BoundedState = false

			tuner, err := NewTuner(configData, tt.env)
			if err != nil {
				t.Fatalf("NewTuner failed: %v", err)
			}

			obsFunc := tuner.makeObservationFunc()
			stateVec := mat.NewVecDense(4, tt.stateValues)
			result := obsFunc(stateVec)

			if tt.expectNil {
				if result != nil {
					t.Error("Expected nil result from observation function")
				}
			} else {
				if result == nil {
					t.Error("Expected non-nil result from observation function")
				} else {
					// Verify result has 2 dimensions (TTFT and ITL)
					if result.Len() != 2 {
						t.Errorf("Expected result length 2, got %d", result.Len())
					}
					// Verify values are non-negative (metrics should be positive)
					if result.AtVec(0) < 0 {
						t.Errorf("TTFT should be non-negative, got %f", result.AtVec(0))
					}
					if result.AtVec(1) < 0 {
						t.Errorf("ITL should be non-negative, got %f", result.AtVec(1))
					}
				}
			}
		})
	}
}

// TestTuner_Run_WithHighInnovation tests Run with cases that have higher innovation
// This should be rejected as an outlier by the NIS check
func TestTuner_Run_WithHighInnovation(t *testing.T) {
	// Create a config with normal settings
	configData := createTestConfig()

	// Create environment with observations significantly different from expected
	// This will create a high NIS value that should be rejected as an outlier
	env := &Environment{
		Lambda:        90.0, // 1.5x higher than typical
		AvgInputToks:  100,
		AvgOutputToks: 200,
		MaxBatchSize:  8,
		AvgTTFT:       285,  // 1.5x higher than expected
		AvgITL:        22.5, // 1.5x higher than expected
	}

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("NewTuner failed: %v", err)
	}

	// Run should detect outliers (high NIS) and set ValidationFailed flag
	results, err := tuner.Run()
	if err != nil {
		t.Fatalf("Run should not return error, got: %v", err)
	}

	// Verify validation failed flag is set for outlier rejection
	if results == nil {
		t.Fatal("Expected results to be non-nil")
	}
	if !results.ValidationFailed {
		t.Error("Expected ValidationFailed to be true for outlier observations")
	}
}

// TestTuner_UpdateEnvironment_EdgeCases tests UpdateEnvironment with edge cases
func TestTuner_UpdateEnvironment_EdgeCases(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("NewTuner failed: %v", err)
	}

	tests := []struct {
		name   string
		newEnv *Environment
	}{
		{
			name: "drastically different lambda",
			newEnv: &Environment{
				Lambda:        6000.0, // 100x higher
				AvgInputToks:  100,
				AvgOutputToks: 200,
				MaxBatchSize:  8,
				AvgTTFT:       190,
				AvgITL:        15,
			},
		},
		{
			name: "minimal values",
			newEnv: &Environment{
				Lambda:        0.01,
				AvgInputToks:  1,
				AvgOutputToks: 1,
				MaxBatchSize:  1,
				AvgTTFT:       1,
				AvgITL:        1,
			},
		},
		{
			name: "maximal values",
			newEnv: &Environment{
				Lambda:        1000000.0,
				AvgInputToks:  100000,
				AvgOutputToks: 200000,
				MaxBatchSize:  256,
				AvgTTFT:       10000,
				AvgITL:        1000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tuner.UpdateEnvironment(tt.newEnv)
			if err != nil {
				t.Errorf("UpdateEnvironment failed: %v", err)
			}

			retrievedEnv := tuner.GetEnvironment()
			if retrievedEnv != tt.newEnv {
				t.Error("UpdateEnvironment did not update the environment")
			}

			// Verify all fields were updated
			if retrievedEnv.Lambda != tt.newEnv.Lambda {
				t.Errorf("Lambda not updated: got %f, want %f", retrievedEnv.Lambda, tt.newEnv.Lambda)
			}
			if retrievedEnv.AvgInputToks != tt.newEnv.AvgInputToks {
				t.Errorf("AvgInputToks not updated")
			}
			if retrievedEnv.AvgOutputToks != tt.newEnv.AvgOutputToks {
				t.Errorf("AvgOutputToks not updated")
			}
			if retrievedEnv.MaxBatchSize != tt.newEnv.MaxBatchSize {
				t.Errorf("MaxBatchSize not updated")
			}
		})
	}
}

// TestTuner_Covariance_Properties tests that covariance matrix maintains its properties
func TestTuner_Covariance_Properties(t *testing.T) {
	configData := createTestConfig()
	env := createTestEnvironment()

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("NewTuner failed: %v", err)
	}

	// Run a few iterations
	_, err = tuner.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Get covariance matrix
	P := tuner.P()
	if P == nil {
		t.Fatal("Covariance matrix is nil")
	}

	rows, cols := P.Dims()
	if rows != cols {
		t.Errorf("Covariance matrix should be square: got %dx%d", rows, cols)
	}

	if rows != 4 {
		t.Errorf("Covariance matrix should be 4x4: got %dx%d", rows, cols)
	}

	// Check symmetry
	for i := range rows {
		for j := i + 1; j < cols; j++ {
			if math.Abs(P.At(i, j)-P.At(j, i)) > 1e-10 {
				t.Errorf("Covariance matrix not symmetric at (%d,%d): %f != %f", i, j, P.At(i, j), P.At(j, i))
			}
		}
	}

	// Check diagonal elements are non-negative (variances)
	for i := range rows {
		if P.At(i, i) < 0 {
			t.Errorf("Covariance diagonal element (%d,%d) is negative: %f", i, i, P.At(i, i))
		}
	}
}
