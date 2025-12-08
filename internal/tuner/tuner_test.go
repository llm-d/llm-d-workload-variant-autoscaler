package tuner

import (
	"fmt"
	"testing"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	infernoConfig "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Test constants for tuner tests, based on queue analyzer predictions
const (
	// Test workload parameters
	TestLambdaReqPerMin = 60.0 // 60 requests per minute
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

	// Test SLO values (target performance)
	TestSLO_TTFT = 500.0 // SLO for time to first token
	TestSLO_ITL  = 24.0  // SLO for inter-token latency

	// Test covariance matrix values (variance for each parameter)
	TestCovAlpha = 0.25     // Variance for alpha
	TestCovBeta  = 0.0625   // Variance for beta
	TestCovGamma = 1.0      // Variance for gamma
	TestCovDelta = 0.000225 // Variance for delta

	// Test NIS value (normalized innovation squared)
	TestNIS = 2.5

	// Test server/model identifiers
	TestModelName       = "test-model"
	TestAcceleratorType = "A100"
	TestServiceClass    = "premium"
	TestVAName          = "test-va"
	TestVANamespace     = "default"
)

// Helper to create test SystemData for tuner.go tests
func createTunerTestSystemData() *infernoConfig.SystemData {
	return &infernoConfig.SystemData{
		Spec: infernoConfig.SystemSpec{
			Models: infernoConfig.ModelData{
				PerfData: []infernoConfig.ModelAcceleratorPerfData{
					{
						Name:     TestModelName,
						Acc:      TestAcceleratorType,
						AccCount: 1,
						DecodeParms: infernoConfig.DecodeParms{
							Alpha: TestAlpha,
							Beta:  TestBeta,
						},
						PrefillParms: infernoConfig.PrefillParms{
							Gamma: TestGamma,
							Delta: TestDelta,
						},
					},
				},
			},
			ServiceClasses: infernoConfig.ServiceClassData{
				Spec: []infernoConfig.ServiceClassSpec{
					{
						Name: TestServiceClass,
						ModelTargets: []infernoConfig.ModelTarget{
							{
								Model:    TestModelName,
								SLO_ITL:  TestSLO_ITL,
								SLO_TTFT: TestSLO_TTFT,
							},
						},
					},
				},
			},
			Servers: infernoConfig.ServerData{
				Spec: []infernoConfig.ServerSpec{
					{
						Name:  TestVAName + ":" + TestVANamespace, // Format: name:namespace
						Model: TestModelName,
						Class: TestServiceClass,
						CurrentAlloc: infernoConfig.AllocationData{
							Accelerator: TestAcceleratorType,
							NumReplicas: 1,
							MaxBatch:    TestMaxBatchSize,
							TTFTAverage: TestPredictedTTFT,
							ITLAverage:  TestPredictedITL,
							Load: infernoConfig.ServerLoadSpec{
								ArrivalRate:  TestLambdaReqPerMin,
								AvgInTokens:  TestAvgInputTokens,
								AvgOutTokens: TestAvgOutputTokens,
							},
						},
					},
				},
			},
		},
	}
} // Helper to create test VA for tuner.go tests
func createTunerTestVA(name, namespace string, activateTuner bool) llmdVariantAutoscalingV1alpha1.VariantAutoscaling {
	return llmdVariantAutoscalingV1alpha1.VariantAutoscaling{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: llmdVariantAutoscalingV1alpha1.VariantAutoscalingSpec{
			ModelID:            TestModelName,
			ActivateModelTuner: activateTuner,
			ModelProfile: llmdVariantAutoscalingV1alpha1.ModelProfile{
				Accelerators: []llmdVariantAutoscalingV1alpha1.AcceleratorProfile{
					{
						Acc:      TestAcceleratorType,
						AccCount: 1,
						PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
							DecodeParms:  map[string]string{"alpha": fmt.Sprintf("%.1f", TestAlpha), "beta": fmt.Sprintf("%.1f", TestBeta)},
							PrefillParms: map[string]string{"gamma": fmt.Sprintf("%.1f", TestGamma), "delta": fmt.Sprintf("%.2f", TestDelta)},
						},
						MaxBatchSize: 4,
					},
				},
			},
		},
	}
}

func TestTuneModelPerfParams_TunerDisabled(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, false)

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)
	if err != nil {
		t.Fatalf("TuneModelPerfParams should not fail when tuner is disabled: %v", err)
	}

	// Verify system data was not modified
	if len(systemData.Spec.Models.PerfData) != 1 {
		t.Error("SystemData should not be modified when tuner is disabled")
	}
}

func TestTuneModelPerfParams_ServerNotFound(t *testing.T) {
	systemData := createTunerTestSystemData()
	// Clear servers
	systemData.Spec.Servers.Spec = []infernoConfig.ServerSpec{}

	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)
	if err != nil {
		t.Fatalf("TuneModelPerfParams should not fail when server not found: %v", err)
	}
}

func TestTuneModelPerfParams_InvalidEnvironment(t *testing.T) {
	systemData := createTunerTestSystemData()
	// Set invalid allocation (zero values)
	systemData.Spec.Servers.Spec[0].CurrentAlloc = infernoConfig.AllocationData{
		Accelerator: TestAcceleratorType,
		NumReplicas: 1,
		MaxBatch:    0, // Invalid
		TTFTAverage: 0, // Invalid
		ITLAverage:  0, // Invalid
		Load: infernoConfig.ServerLoadSpec{
			ArrivalRate:  0, // Invalid
			AvgInTokens:  0, // Invalid
			AvgOutTokens: 0, // Invalid
		},
	}

	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)
	if err != nil {
		t.Fatalf("TuneModelPerfParams should not fail with invalid environment: %v", err)
	}
}

func TestTuneModelPerfParams_ValidTuning(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)
	// NIS validation should pass
	if err != nil {
		t.Fatalf("Tuning returned error (may be expected during initial calibration): %v", err)
	}

	// Check if SystemData was updated (tuning succeeded)
	if len(systemData.Spec.Models.PerfData) > 0 {
		perfData := systemData.Spec.Models.PerfData[0]
		t.Logf("After tuning - Alpha: %f, Beta: %f, Gamma: %f, Delta: %f",
			perfData.DecodeParms.Alpha,
			perfData.DecodeParms.Beta,
			perfData.PrefillParms.Gamma,
			perfData.PrefillParms.Delta)
	}
}

func TestTuneModelPerfParams_SuccessfulTuningPath(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Add existing tuned state to VA - using actual predictions from queue analyzer
	// With params [5.0, 2.5, 10.0, 0.15], lambda=1 req/sec, batch=8, the analyzer predicts:
	// TTFT=186.7ms, ITL=14.9ms
	va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{
		Model:       TestModelName,
		Accelerator: TestAcceleratorType,
		PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
			DecodeParms:  map[string]string{"alpha": fmt.Sprintf("%.1f", TestAlpha), "beta": fmt.Sprintf("%.1f", TestBeta)},
			PrefillParms: map[string]string{"gamma": fmt.Sprintf("%.1f", TestGamma), "delta": fmt.Sprintf("%.2f", TestDelta)},
		},
		NIS: fmt.Sprintf("%.1f", TestNIS),
		CovarianceMatrix: [][]string{
			{fmt.Sprintf("%.2f", TestCovAlpha), "0.0", "0.0", "0.0"},
			{"0.0", fmt.Sprintf("%.4f", TestCovBeta), "0.0", "0.0"},
			{"0.0", "0.0", fmt.Sprintf("%.1f", TestCovGamma), "0.0"},
			{"0.0", "0.0", "0.0", fmt.Sprintf("%.6f", TestCovDelta)},
		},
	}

	// Use observations that match what the current model predicts (from queue analyzer)
	// This ensures NIS validation passes since observations are consistent with the model
	// Current state [5.0, 2.5, 10.0, 0.15] predicts TTFT=186.7, ITL=14.9
	// We use slightly different values to simulate small measurement variations
	systemData.Spec.Servers.Spec[0].CurrentAlloc.TTFTAverage = 188.0 // Close to predicted 186.7
	systemData.Spec.Servers.Spec[0].CurrentAlloc.ITLAverage = 15.2   // Close to predicted 14.9

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)
	if err != nil {
		t.Logf("Tuning with adjusted metrics returned error: %v", err)
		// Even with adjusted metrics, NIS rejection is possible
	}

	// Verify SystemData was potentially updated
	perfData := systemData.Spec.Models.PerfData[0]
	t.Logf("Final params - Alpha: %f, Beta: %f, Gamma: %f, Delta: %f",
		perfData.DecodeParms.Alpha,
		perfData.DecodeParms.Beta,
		perfData.PrefillParms.Gamma,
		perfData.PrefillParms.Delta)

	// Verify VA status was potentially updated
	if va.Status.TunerPerfData != nil && va.Status.TunerPerfData.Model != "" {
		t.Logf("VA status updated with model: %s, accelerator: %s",
			va.Status.TunerPerfData.Model,
			va.Status.TunerPerfData.Accelerator)
	}
}

func TestTuneModelPerfParams_MultipleVAs(t *testing.T) {
	systemData := createTunerTestSystemData()

	// Add multiple servers
	systemData.Spec.Servers.Spec = append(systemData.Spec.Servers.Spec,
		infernoConfig.ServerSpec{
			Name:  "test-va2/default",
			Model: TestModelName,
			Class: "premium",
			CurrentAlloc: infernoConfig.AllocationData{
				Accelerator: TestAcceleratorType,
				NumReplicas: 1,
				MaxBatch:    4,
				TTFTAverage: 200,
				ITLAverage:  16,
				Load: infernoConfig.ServerLoadSpec{
					ArrivalRate:  65.0,
					AvgInTokens:  110,
					AvgOutTokens: 210,
				},
			},
		},
	)

	va1 := createTunerTestVA(TestVAName, TestVANamespace, true)
	va2 := createTunerTestVA("test-va2", "default", false)

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va1, va2}, systemData, false)
	if err != nil {
		t.Fatalf("TuneModelPerfParams should handle multiple VAs: %v", err)
	}
}

func TestTuneServer_InvalidEnvironment(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Server with invalid environment
	server := &infernoConfig.ServerSpec{
		Name:  "test-va/default",
		Model: TestModelName,
		Class: "premium",
		CurrentAlloc: infernoConfig.AllocationData{
			Accelerator: TestAcceleratorType,
			Load: infernoConfig.ServerLoadSpec{
				ArrivalRate: 0, // Invalid
			},
		},
	}

	_, err := tuneServer(&va, systemData, server, false)
	if err == nil {
		t.Error("tuneServer should fail with invalid environment")
	}
}

func TestTuneServer_ValidEnvironment(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	server := &systemData.Spec.Servers.Spec[0]

	_, err := tuneServer(&va, systemData, server, false)
	// May succeed or fail depending on NIS validation
	if err != nil {
		t.Logf("tuneServer returned error (may be expected): %v", err)
	}
}

func TestTuneServer_MissingSLO(t *testing.T) {
	systemData := createTunerTestSystemData()
	// Clear SLO data
	systemData.Spec.ServiceClasses.Spec[0].ModelTargets = []infernoConfig.ModelTarget{}

	va := createTunerTestVA(TestVAName, TestVANamespace, true)
	server := &systemData.Spec.Servers.Spec[0]

	_, err := tuneServer(&va, systemData, server, false)
	if err == nil {
		t.Error("tuneServer should fail when SLO not found")
	}
}

func TestTuneServer_WithExistingTunedResults(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Add existing tuned results to VA status
	va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{
		Model:       TestModelName,
		Accelerator: TestAcceleratorType,
		PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
			DecodeParms:  map[string]string{"alpha": "5.5", "beta": "2.7"},
			PrefillParms: map[string]string{"gamma": "10.5", "delta": "0.16"},
		},
		NIS: "3.5",
		CovarianceMatrix: [][]string{
			{"0.1", "0.0", "0.0", "0.0"},
			{"0.0", "0.1", "0.0", "0.0"},
			{"0.0", "0.0", "0.1", "0.0"},
			{"0.0", "0.0", "0.0", "0.1"},
		},
	}

	server := &systemData.Spec.Servers.Spec[0]

	_, err := tuneServer(&va, systemData, server, false)
	// May succeed or fail depending on NIS validation
	if err != nil {
		t.Logf("tuneServer with existing results returned error: %v", err)
	}
}

func TestCreateTuner_Success(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)
	server := &systemData.Spec.Servers.Spec[0]

	tuner, err := createTuner(&va, systemData, server, false)
	if err != nil {
		t.Fatalf("createTuner should succeed with valid inputs: %v", err)
	}

	if tuner == nil {
		t.Error("createTuner should return non-nil tuner")
	}
}

func TestCreateTuner_MissingSLO(t *testing.T) {
	systemData := createTunerTestSystemData()
	// Clear SLO
	systemData.Spec.ServiceClasses.Spec[0].ModelTargets = []infernoConfig.ModelTarget{}

	va := createTunerTestVA(TestVAName, TestVANamespace, true)
	server := &systemData.Spec.Servers.Spec[0]

	_, err := createTuner(&va, systemData, server, false)
	if err == nil {
		t.Error("createTuner should fail when SLO not found")
	}
}

func TestCreateTuner_InvalidEnvironment(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Server with invalid environment
	server := &infernoConfig.ServerSpec{
		Name:  "test-va/default",
		Model: TestModelName,
		Class: "premium",
		CurrentAlloc: infernoConfig.AllocationData{
			Accelerator: TestAcceleratorType,
			Load: infernoConfig.ServerLoadSpec{
				ArrivalRate: 0, // Invalid
			},
		},
	}

	_, err := createTuner(&va, systemData, server, false)
	if err == nil {
		t.Error("createTuner should fail with invalid environment")
	}
}

func TestCreateTuner_WithExistingState(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Add existing tuned state
	va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{
		Model:       TestModelName,
		Accelerator: TestAcceleratorType,
		PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
			DecodeParms:  map[string]string{"alpha": "5.5", "beta": "2.7"},
			PrefillParms: map[string]string{"gamma": "10.5", "delta": "0.16"},
		},
		CovarianceMatrix: [][]string{
			{"0.1", "0.0", "0.0", "0.0"},
			{"0.0", "0.1", "0.0", "0.0"},
			{"0.0", "0.0", "0.1", "0.0"},
			{"0.0", "0.0", "0.0", "0.1"},
		},
	}

	server := &systemData.Spec.Servers.Spec[0]

	tuner, err := createTuner(&va, systemData, server, false)
	if err != nil {
		t.Fatalf("createTuner should succeed with existing state: %v", err)
	}

	if tuner == nil {
		t.Error("createTuner should return non-nil tuner")
	}
}

func TestCreateTuner_ExtremelyHighLambda(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Set extremely high lambda that causes QueueAnalyzer to fail
	server := &infernoConfig.ServerSpec{
		Name:  "test-va/default",
		Model: TestModelName,
		Class: "premium",
		CurrentAlloc: infernoConfig.AllocationData{
			Accelerator: TestAcceleratorType,
			NumReplicas: 1,
			MaxBatch:    4,
			TTFTAverage: TestPredictedTTFT,
			ITLAverage:  TestPredictedITL,
			Load: infernoConfig.ServerLoadSpec{
				ArrivalRate:  1000000.0, // Extremely high
				AvgInTokens:  TestAvgInputTokens,
				AvgOutTokens: TestAvgOutputTokens,
			},
		},
	}

	_, err := createTuner(&va, systemData, server, false)
	if err == nil {
		t.Error("createTuner should fail with extremely high lambda")
	}
}

func TestTuneModelPerfParams_NISValidationFailure(t *testing.T) {
	systemData := createTunerTestSystemData()
	va := createTunerTestVA(TestVAName, TestVANamespace, true)

	// Add existing tuned state with tight covariance
	// This makes the filter very sensitive to deviations
	va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{
		Model:       TestModelName,
		Accelerator: TestAcceleratorType,
		PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
			DecodeParms:  map[string]string{"alpha": fmt.Sprintf("%.1f", TestAlpha), "beta": fmt.Sprintf("%.1f", TestBeta)},
			PrefillParms: map[string]string{"gamma": fmt.Sprintf("%.1f", TestGamma), "delta": fmt.Sprintf("%.2f", TestDelta)},
		},
		NIS: fmt.Sprintf("%.1f", TestNIS),
		CovarianceMatrix: [][]string{
			{"0.001", "0.0", "0.0", "0.0"},    // Very tight covariance
			{"0.0", "0.001", "0.0", "0.0"},    // Very tight covariance
			{"0.0", "0.0", "0.001", "0.0"},    // Very tight covariance
			{"0.0", "0.0", "0.0", "0.000001"}, // Very tight covariance
		},
	}

	// Use observations that are significantly different from predictions
	// Current state [5.0, 2.5, 10.0, 0.15] predicts TTFT=186.7, ITL=14.9
	// Set observations far from predictions to trigger NIS failure
	systemData.Spec.Servers.Spec[0].CurrentAlloc.TTFTAverage = 450.0
	systemData.Spec.Servers.Spec[0].CurrentAlloc.ITLAverage = 23.0

	// Store original parameters to verify they remain unchanged after NIS failure
	originalAlpha := systemData.Spec.Models.PerfData[0].DecodeParms.Alpha
	originalBeta := systemData.Spec.Models.PerfData[0].DecodeParms.Beta
	originalGamma := systemData.Spec.Models.PerfData[0].PrefillParms.Gamma
	originalDelta := systemData.Spec.Models.PerfData[0].PrefillParms.Delta

	err := TuneModelPerfParams([]llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, systemData, false)

	// Should not return error - NIS failure is handled gracefully
	if err != nil {
		t.Fatalf("TuneModelPerfParams should not return error on NIS failure: %v", err)
	}

	// Verify parameters remain unchanged (previous state used)
	perfData := systemData.Spec.Models.PerfData[0]
	if perfData.DecodeParms.Alpha != originalAlpha {
		t.Errorf("Alpha should remain unchanged after NIS failure: got %.6f, want %.6f",
			perfData.DecodeParms.Alpha, originalAlpha)
	}
	if perfData.DecodeParms.Beta != originalBeta {
		t.Errorf("Beta should remain unchanged after NIS failure: got %.6f, want %.6f",
			perfData.DecodeParms.Beta, originalBeta)
	}
	if perfData.PrefillParms.Gamma != originalGamma {
		t.Errorf("Gamma should remain unchanged after NIS failure: got %.6f, want %.6f",
			perfData.PrefillParms.Gamma, originalGamma)
	}
	if perfData.PrefillParms.Delta != originalDelta {
		t.Errorf("Delta should remain unchanged after NIS failure: got %.6f, want %.6f",
			perfData.PrefillParms.Delta, originalDelta)
	}

	// Verify VA status still has the previous NIS value
	if va.Status.TunerPerfData != nil && va.Status.TunerPerfData.NIS != fmt.Sprintf("%.1f", TestNIS) {
		t.Errorf("NIS should remain at previous value after validation failure: got %s, want %.1f",
			va.Status.TunerPerfData.NIS, TestNIS)
	}

	t.Logf("NIS validation failed as expected - parameters preserved at: Alpha=%.6f, Beta=%.6f, Gamma=%.6f, Delta=%.6f",
		perfData.DecodeParms.Alpha,
		perfData.DecodeParms.Beta,
		perfData.PrefillParms.Gamma,
		perfData.PrefillParms.Delta)
}
