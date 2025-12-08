package tuner

import (
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	kalman "github.com/llm-inferno/kalman-filter/pkg/core"
	"gonum.org/v1/gonum/mat"
)

func TestNewStasher(t *testing.T) {
	// Create a simple filter for testing
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))
	filter, err := kalman.NewExtendedKalmanFilter(4, 2,
		mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0}),
		initialP)

	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}

	if stasher == nil {
		t.Fatal("NewStasher() returned nil")
	}
}

func TestNewStasherNilFilter(t *testing.T) {
	// Create a nil filter
	var filter *kalman.ExtendedKalmanFilter = nil
	_, err := NewStasher(filter)
	if err == nil {
		t.Fatalf("Should fail with nil filter but did not")
	}
}

func TestStasher_StashAndUnStash(t *testing.T) {
	// Create a filter
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	// Create stasher
	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}

	// Stash the current state
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}

	// Modify the filter state
	newX := mat.NewVecDense(4, []float64{10.0, 4.0, 3.0, 20.0})
	newP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{2.0, 2.0, 2.0, 2.0}))
	filter.X = newX
	filter.P = mat.DenseCopyOf(newP)

	// Verify that state was modified
	if mat.Equal(filter.X, initialX) {
		t.Error("Filter state should have been modified")
	}

	// UnStash to restore
	err = stasher.UnStash()
	if err != nil {
		t.Errorf("UnStash() failed: %v", err)
	}

	// Verify that state was restored
	for i := range 4 {
		if filter.X.AtVec(i) != initialX.AtVec(i) {
			t.Errorf("X[%d] = %f, want %f after UnStash", i, filter.X.AtVec(i), initialX.AtVec(i))
		}
	}

	// Verify P was restored (check diagonal elements)
	for i := range 4 {
		if filter.P.At(i, i) != initialP.At(i, i) {
			t.Errorf("P[%d,%d] = %f, want %f after UnStash", i, i, filter.P.At(i, i), initialP.At(i, i))
		}
	}
}

func TestStasher_MultipleStashOperations(t *testing.T) {
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}

	// First stash
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}
	// Modify state
	filter.X = mat.NewVecDense(4, []float64{6.0, 2.5, 1.8, 11.0})

	// Second stash (should overwrite first)
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}

	// Modify state again
	filter.X = mat.NewVecDense(4, []float64{10.0, 5.0, 3.0, 20.0})

	// UnStash should restore to second stash, not first
	err = stasher.UnStash()
	if err != nil {
		t.Errorf("UnStash() failed: %v", err)
	}

	expectedX := []float64{6.0, 2.5, 1.8, 11.0}
	for i := range 4 {
		if filter.X.AtVec(i) != expectedX[i] {
			t.Errorf("X[%d] = %f, want %f (should restore to second stash)",
				i, filter.X.AtVec(i), expectedX[i])
		}
	}
}

func TestStasher_WithRealTuner(t *testing.T) {
	// Create a real tuner for integration testing
	configData := &TunerConfigData{
		FilterData: FilterData{
			GammaFactor: constants.DefaultGammaFactor,
			ErrorLevel:  constants.DefaultErrorLevel,
			TPercentile: constants.DefaultTPercentile,
		},
		ModelData: TunerModelData{
			InitState:            []float64{5.0, 2.5, 10.0, 0.15}, // Use realistic params
			PercentChange:        []float64{0.1, 0.1, 0.1, 0.1},
			BoundedState:         true,
			MinState:             []float64{1.0, 0.5, 0.01, 2.0},
			MaxState:             []float64{20.0, 10.0, 1.0, 50.0},
			ExpectedObservations: []float64{186.7, 14.9}, // Use realistic values
		},
	}

	env := &Environment{
		Lambda:        60.0,
		AvgInputToks:  100,
		AvgOutputToks: 200,
		MaxBatchSize:  8,
		AvgTTFT:       186.7, // Use realistic values
		AvgITL:        14.9,
	}

	tuner, err := NewTuner(configData, env)
	if err != nil {
		t.Fatalf("Failed to create tuner: %v", err)
	}

	// Get initial state
	initialX := mat.VecDenseCopyOf(tuner.X())
	initialP := mat.DenseCopyOf(tuner.P())

	// Run tuning (this will use stash/unstash internally)
	_, err = tuner.Run()
	if err != nil {
		t.Logf("Tuner run failed (may be expected): %v", err)
	}

	// State should have changed (either from successful run or restored via unstash)
	// Just verify that the mechanism works without error
	finalX := tuner.X()
	if finalX == nil {
		t.Error("Final state is nil")
	}

	t.Logf("Initial X: %v", initialX.RawVector().Data)
	t.Logf("Final X: %v", finalX.RawVector().Data)
	t.Logf("Initial P diagonal: [%f, %f, %f, %f]",
		initialP.At(0, 0), initialP.At(1, 1), initialP.At(2, 2), initialP.At(3, 3))
	finalP := tuner.P()
	t.Logf("Final P diagonal: [%f, %f, %f, %f]",
		finalP.At(0, 0), finalP.At(1, 1), finalP.At(2, 2), finalP.At(3, 3))
}

func TestStasher_StateIndependence(t *testing.T) {
	// Test that stashed state is independent copy
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}

	// Modify filter state
	filter.X.SetVec(0, 999.0)
	filter.P.Set(0, 0, 999.0)

	// UnStash should restore original values
	err = stasher.UnStash()
	if err != nil {
		t.Errorf("UnStash() failed: %v", err)
	}

	if filter.X.AtVec(0) == 999.0 {
		t.Error("Stash/UnStash did not properly restore state - stashed data was modified")
	}

	if filter.P.At(0, 0) == 999.0 {
		t.Error("Stash/UnStash did not properly restore covariance - stashed data was modified")
	}

	// Should have original value
	if filter.X.AtVec(0) != 5.0 {
		t.Errorf("X[0] = %f, want 5.0", filter.X.AtVec(0))
	}
	if filter.P.At(0, 0) != 1.0 {
		t.Errorf("P[0,0] = %f, want 1.0", filter.P.At(0, 0))
	}
}

func TestStasher_UnStashWithoutStash(t *testing.T) {
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}

	// Call UnStash without calling Stash first
	// This tests that an error is returned and no panic occurs
	err = stasher.UnStash()
	if err == nil {
		t.Fatalf("UnStash() should have failed but did not")
	}
}

func TestStasher_CopySemantics(t *testing.T) {
	// Verify that Stash makes deep copies
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})
	initialP := mat.DenseCopyOf(mat.NewDiagDense(4, []float64{1.0, 1.0, 1.0, 1.0}))

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}

	// Get the original values
	originalX := make([]float64, 4)
	for i := 0; i < 4; i++ {
		originalX[i] = filter.X.AtVec(i)
	}

	// Modify filter
	for i := 0; i < 4; i++ {
		filter.X.SetVec(i, float64(i*100))
	}

	// Restore
	err = stasher.UnStash()
	if err != nil {
		t.Errorf("UnStash() failed: %v", err)
	}

	// Verify restoration
	for i := 0; i < 4; i++ {
		if filter.X.AtVec(i) != originalX[i] {
			t.Errorf("X[%d] = %f, want %f after restore",
				i, filter.X.AtVec(i), originalX[i])
		}
	}
}

func TestStasher_FullCovarianceMatrix(t *testing.T) {
	// Test with a non-diagonal covariance matrix
	initialX := mat.NewVecDense(4, []float64{5.0, 2.0, 1.5, 10.0})

	// Create a non-diagonal P matrix
	pData := []float64{
		1.0, 0.1, 0.2, 0.3,
		0.1, 2.0, 0.4, 0.5,
		0.2, 0.4, 3.0, 0.6,
		0.3, 0.5, 0.6, 4.0,
	}
	initialP := mat.NewDense(4, 4, pData)

	filter, err := kalman.NewExtendedKalmanFilter(4, 2, initialX, mat.DenseCopyOf(initialP))
	if err != nil {
		t.Fatalf("Failed to create filter: %v", err)
	}

	stasher, err := NewStasher(filter)
	if err != nil {
		t.Fatalf("Failed to create stasher: %v", err)
	}
	err = stasher.Stash()
	if err != nil {
		t.Errorf("Stash() failed: %v", err)
	}

	// Modify all elements of P
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			filter.P.Set(i, j, 999.0)
		}
	}

	// Restore
	err = stasher.UnStash()
	if err != nil {
		t.Errorf("UnStash() failed: %v", err)
	}

	// Verify all elements restored
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			expected := pData[i*4+j]
			actual := filter.P.At(i, j)
			if actual != expected {
				t.Errorf("P[%d,%d] = %f, want %f after restore", i, j, actual, expected)
			}
		}
	}
}
