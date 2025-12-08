package tuner

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	infernoConfig "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	tune "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/tuner"
	"gonum.org/v1/gonum/mat"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrorInsufficientMetrics indicates that tuning was skipped due to insufficient metrics (e.g., no traffic).
var ErrorInsufficientMetrics = errors.New("insufficient metrics for tuning")

// build config data from defaults, init state and slos
func BuildTunerConfig(
	state []float64,
	covMatrix []float64,
	slos []float64,
) (*tune.TunerConfigData, error) {

	if len(state) == 0 || len(slos) == 0 {
		return nil, fmt.Errorf("initState and slos must be non-empty")
	}
	return &tune.TunerConfigData{
		FilterData: getDefaultFilterData(),
		ModelData: tune.TunerModelData{
			InitState:            state,
			InitCovarianceMatrix: covMatrix,
			PercentChange:        getDefaultPercentChange(),
			BoundedState:         true,
			MinState:             getFactoredState(state, constants.DefaultMinStateFactor),
			MaxState:             getFactoredState(state, constants.DefaultMaxStateFactor),
			ExpectedObservations: slos,
		},
	}, nil
}

func getDefaultFilterData() tune.FilterData {
	return tune.FilterData{
		GammaFactor: constants.DefaultGammaFactor,
		ErrorLevel:  constants.DefaultErrorLevel,
		TPercentile: constants.DefaultTPercentile,
	}
}

func getDefaultPercentChange() []float64 {
	return []float64{
		constants.DefaultPercentChange, // alpha variance
		constants.DefaultPercentChange, // beta variance
		constants.DefaultPercentChange, // gamma variance
		constants.DefaultPercentChange, // delta variance
	}
}

// multiply each element in initState by multiplier and returns the new slice
func getFactoredState(initState []float64, multiplier float64) []float64 {
	multipliedNumbers := make([]float64, len(initState))
	for i, num := range initState {
		multipliedNumbers[i] = num * multiplier
	}
	return multipliedNumbers
}

// convertAllocToEnvironment converts WVA CurrentAlloc to model-tuner Environment.
// This is the adapter between the WVA collector and the Kalman filter tuner.
// Returns a nil Environment and error if the allocation data is invalid.
func convertAllocToEnvironment(alloc infernoConfig.AllocationData) (*tune.Environment, error) {
	// Validate inputs to prevent division by zero and invalid state
	if alloc.NumReplicas <= 0 {
		logger.Log.Warnf("Invalid allocation: NumReplicas must be positive, got %d", alloc.NumReplicas)
		return nil, fmt.Errorf("invalid allocation: NumReplicas must be positive")
	}
	if alloc.Load.ArrivalRate < 0 {
		logger.Log.Warnf("Invalid allocation: ArrivalRate must be greater or equal to 0, got %f", alloc.Load.ArrivalRate)
		return nil, fmt.Errorf("invalid allocation: ArrivalRate must be greater or equal to 0")
	}
	if alloc.MaxBatch <= 0 {
		logger.Log.Warnf("Invalid allocation: MaxBatch must be positive, got %d", alloc.MaxBatch)
		return nil, fmt.Errorf("invalid allocation: MaxBatch must be positive")
	}

	// Calculate request rate per replica
	ratePerReplica := alloc.Load.ArrivalRate / float32(alloc.NumReplicas)

	return &tune.Environment{
		Lambda:        ratePerReplica,
		AvgOutputToks: alloc.Load.AvgOutTokens,
		AvgInputToks:  alloc.Load.AvgInTokens,
		MaxBatchSize:  alloc.MaxBatch,
		AvgTTFT:       alloc.TTFTAverage,
		AvgITL:        alloc.ITLAverage,
		NumReplicas:   alloc.NumReplicas,
	}, nil
}

// getInitialStateWithFallback attempts to get initial state using the priority defined by autoGuessInitialState:
// - If autoGuessInitialState=true: guess -> spec
// - If autoGuessInitialState=false: spec -> guess
func getInitialStateWithFallback(
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) ([]float64, error) {
	if autoGuessInitialState {
		// Try guess first, fallback to spec
		state, err := guessInitState(server)
		if err == nil {
			return state, nil
		}

		state, err = findStateInSystemData(systemData, server.Model, server.CurrentAlloc.Accelerator)
		if err != nil {
			return nil, fmt.Errorf("failed to get state from guess and spec: %w", err)
		}
		return state, nil
	}

	// Try spec first, fallback to guess
	state, err := findStateInSystemData(systemData, server.Model, server.CurrentAlloc.Accelerator)
	if err == nil {
		return state, nil
	}

	state, err = guessInitState(server)
	if err != nil {
		return nil, fmt.Errorf("failed to get state from spec and guess: %w", err)
	}
	return state, nil
}

// get state and covariance matrix from VA status (if exist), otherwise return only the state params from VA spec
func getStateAndCovariance(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) (state, covMatrix []float64, err error) {
	// 1. Check if VA has tuned results in status (i.e. has been tuned before)
	if HasFullTunedResults(va) {
		state, covMatrix, err = extractStateAndCovarianceFromVAStatus(va)
		if err != nil {
			logger.Log.Warnf("Failed to extract tuned state from VA status, falling back for variant %s: %v",
				va.Name,
				err)
		} else {
			// Status extracted: using it for tuning
			logger.Log.Debugf("Using state vals from VA status to tune variant %s: alpha= %.6f, beta= %.6f, gamma= %.6f, delta= %.6f",
				va.Name,
				state[constants.StateIndexAlpha],
				state[constants.StateIndexBeta],
				state[constants.StateIndexGamma],
				state[constants.StateIndexDelta])
			return state, covMatrix, nil
		}
	}

	// No previously tuned results in status, or extraction failed
	// 2. Try to get state from VA spec or by guessing from collected metrics
	state, err = getInitialStateWithFallback(systemData, server, autoGuessInitialState)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get initial state from all sources: %w", err)
	}
	return state, nil, nil

}

// HasFullTunedResults checks if VA status contains both valid tuned parameters and the covariance matrix.
// Used when we need to continue tuning with the full state (including covariance).
func HasFullTunedResults(va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) bool {
	if va.Status.TunerPerfData == nil {
		return false
	}

	perfParms := va.Status.TunerPerfData.PerfParms

	// Check if all 4 parameters are valid
	if !hasValidParams(perfParms.DecodeParms, perfParms.PrefillParms) {
		return false
	}

	// Check covariance matrix has correct dimensions (4x4)
	covMatrix := va.Status.TunerPerfData.CovarianceMatrix
	if len(covMatrix) != 4 {
		return false
	}
	for _, row := range covMatrix {
		if len(row) != 4 {
			return false
		}
	}

	return true
}

// hasValidParams validates that decode and prefill parameter maps contain valid alpha, beta, gamma, delta values
func hasValidParams(decodeParms, prefillParms map[string]string) bool {
	// Check decode parameters exist and have correct keys
	if len(decodeParms) != 2 {
		return false
	}
	alpha, hasAlpha := decodeParms["alpha"]
	if !hasAlpha {
		return false
	}
	beta, hasBeta := decodeParms["beta"]
	if !hasBeta {
		return false
	}

	// Validate alpha and beta values
	if alphaVal, err := strconv.ParseFloat(alpha, 64); err != nil || alphaVal <= 0 {
		return false
	}
	if betaVal, err := strconv.ParseFloat(beta, 64); err != nil || betaVal < 0 {
		return false
	}

	// Check prefill parameters exist and have correct keys
	if len(prefillParms) != 2 {
		return false
	}
	gamma, hasGamma := prefillParms["gamma"]
	if !hasGamma {
		return false
	}
	delta, hasDelta := prefillParms["delta"]
	if !hasDelta {
		return false
	}

	// Validate gamma and delta values
	if gammaVal, err := strconv.ParseFloat(gamma, 64); err != nil || gammaVal <= 0 {
		return false
	}
	if deltaVal, err := strconv.ParseFloat(delta, 64); err != nil || deltaVal < 0 {
		return false
	}

	return true
}

// extractStateAndCovarianceFromVAStatus extracts the state params (alpha, beta, gamma , delta) and the covariance matrix from the VA status
// uses extractStateFromVAStatus and extractCovarianceFromVAStatus helper functions to perform the extraction and parsing
func extractStateAndCovarianceFromVAStatus(va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) (state, covMatrix []float64, err error) {
	state, err = extractStateFromVAStatus(va)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract tuned state from VA status: %w", err)
	}

	covMatrix, err = extractCovarianceFromVAStatus(va)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract covariance matrix from VA status: %w", err)
	}

	return state, covMatrix, nil
}

func extractCovarianceFromVAStatus(va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) ([]float64, error) {
	if va.Status.TunerPerfData == nil {
		return nil, fmt.Errorf("TunerPerfData is nil")
	}

	matStatus := va.Status.TunerPerfData.CovarianceMatrix
	numRows := len(matStatus)
	if numRows == 0 {
		return nil, fmt.Errorf("empty covariance matrix")
	}
	numCols := len(matStatus[0])
	if numRows != numCols || numRows != 4 {
		return nil, fmt.Errorf("invalid covariance matrix dimensions: expected 4 rows and 4 cols, got %d x %d", numRows, numCols)
	}

	// Create a flat slice to hold the float64 data
	data := make([]float64, numRows*numCols)

	// Populate the data slice by parsing strings
	for r := range numRows {
		if len(matStatus[r]) != numCols {
			return nil, fmt.Errorf("row %d has inconsistent column count", r)
		}
		for c := range numCols {
			val, err := strconv.ParseFloat(matStatus[r][c], 64)
			if err != nil {
				return nil, fmt.Errorf("error parsing string '%s' to float64: %v", matStatus[r][c], err)
			}
			data[r*numCols+c] = val
		}
	}

	// Validate that the covariance matrix is symmetric
	covMat := mat.NewDense(numRows, numCols, data)
	if !tune.IsSymmetric(covMat, tune.DefaultEpsilon) {
		logger.Log.Warnf("Covariance matrix from VA status %s/%s is not symmetric, rejecting",
			va.Name, va.Namespace)
		return nil, fmt.Errorf("covariance matrix is not symmetric (tolerance=%.2e)", tune.DefaultEpsilon)
	}

	return data, nil
}

func extractStateFromVAStatus(va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) ([]float64, error) {
	if va.Status.TunerPerfData == nil {
		return nil, fmt.Errorf("TunerPerfData is nil")
	}

	// extract decode model (itl) parameters
	decodeParms := va.Status.TunerPerfData.PerfParms.DecodeParms
	if len(decodeParms) != 2 {
		return nil, fmt.Errorf("length of tuned decode parms in VA status should be 2")
	}
	alpha, err := strconv.ParseFloat(decodeParms["alpha"], 64)
	if err != nil {
		return nil, err
	}
	beta, err := strconv.ParseFloat(decodeParms["beta"], 64)
	if err != nil {
		return nil, err
	}

	// extract prefill model (ttft) parameters
	prefillParms := va.Status.TunerPerfData.PerfParms.PrefillParms
	if len(prefillParms) != 2 {
		return nil, fmt.Errorf("length of prefillParms should be 2")
	}
	gamma, err := strconv.ParseFloat(prefillParms["gamma"], 64)
	if err != nil {
		return nil, err
	}
	delta, err := strconv.ParseFloat(prefillParms["delta"], 64)
	if err != nil {
		return nil, err
	}

	return []float64{alpha, beta, gamma, delta}, nil
}

func findStateInSystemData(
	systemData *infernoConfig.SystemData,
	modelName string,
	acceleratorName string,
) ([]float64, error) {

	for _, perfData := range systemData.Spec.Models.PerfData {
		if perfData.Name == modelName && perfData.Acc == acceleratorName {
			alpha := float64(perfData.DecodeParms.Alpha)
			beta := float64(perfData.DecodeParms.Beta)
			gamma := float64(perfData.PrefillParms.Gamma)
			delta := float64(perfData.PrefillParms.Delta)

			// Validate all parameters are positive
			if alpha <= 0 || beta <= 0 || gamma <= 0 || delta <= 0 {
				return nil, fmt.Errorf("invalid parameters: alpha=%f, beta=%f, gamma=%f, delta=%f (must be positive)",
					alpha, beta, gamma, delta)
			}

			return []float64{alpha, beta, gamma, delta}, nil
		}
	}
	return nil, fmt.Errorf("model %q with accelerator %q not found in system data", modelName, acceleratorName)
}

// make an initial guess of the state estimates based on observed metrics
func guessInitState(serverSpec *infernoConfig.ServerSpec) ([]float64, error) {

	// get observed data
	allocData := &serverSpec.CurrentAlloc
	numReplicas := allocData.NumReplicas
	avgITL := allocData.ITLAverage
	avgTTFT := allocData.TTFTAverage
	rpmTotal := allocData.Load.ArrivalRate
	inTokens := allocData.Load.AvgInTokens
	outTokens := allocData.Load.AvgOutTokens

	if numReplicas <= 0 || avgITL < 0 || avgTTFT < 0 || (avgITL+avgTTFT) == 0 ||
		rpmTotal <= 0 || inTokens <= 0 || outTokens <= 0 {
		return nil, fmt.Errorf("invalid allocation data for server %s: %v", serverSpec.Name, allocData)
	}

	// use msec as time unit
	lambda := rpmTotal / float32(numReplicas) / 60 / 1000
	avgLatency := avgTTFT + avgITL*float32(outTokens)
	avgConcurrency := lambda * avgLatency

	// make initial guess
	alpha := constants.BaseFactor * avgITL
	beta := (avgITL - alpha) / avgConcurrency
	gamma := constants.BaseFactor * avgTTFT
	delta := (avgTTFT - gamma) / avgConcurrency / float32(inTokens)

	state := make([]float64, 4)
	state[constants.StateIndexAlpha] = float64(alpha)
	state[constants.StateIndexBeta] = float64(beta)
	state[constants.StateIndexGamma] = float64(gamma)
	state[constants.StateIndexDelta] = float64(delta)
	return state, nil
}

func findSLOInSystemData(
	systemData *infernoConfig.SystemData,
	modelName string,
	serviceClassName string,
) ([]float64, error) {
	var svcSpecs *infernoConfig.ServiceClassSpec
	for _, spec := range systemData.Spec.ServiceClasses.Spec {
		if spec.Name == serviceClassName {
			svcSpecs = &spec
			break
		}
	}

	if svcSpecs == nil {
		return nil, fmt.Errorf("service class %q not found in system data", serviceClassName)
	}

	for _, modelTarget := range svcSpecs.ModelTargets {
		if modelTarget.Model == modelName {
			sloTTFT := float64(modelTarget.SLO_TTFT)
			sloITL := float64(modelTarget.SLO_ITL)

			// Validate SLOs are positive
			if sloTTFT <= 0 || sloITL <= 0 {
				return nil, fmt.Errorf("invalid SLOs for model %q: TTFT=%f, ITL=%f (must be positive)",
					modelName, sloTTFT, sloITL)
			}

			return []float64{sloTTFT, sloITL}, nil
		}
	}
	return nil, fmt.Errorf("model %q not found in service class %q", modelName, serviceClassName)
}

// updates queueing model parameters in the system data
func updateModelPerfDataInSystemData(systemData *infernoConfig.SystemData, modelName, accName string, tunedResults *tune.TunedResults) error {
	for i := range systemData.Spec.Models.PerfData {
		perfData := &systemData.Spec.Models.PerfData[i]
		if perfData.Name == modelName && perfData.Acc == accName {
			perfData.DecodeParms.Alpha = float32(tunedResults.ServiceParms.Decode.Alpha)
			perfData.DecodeParms.Beta = float32(tunedResults.ServiceParms.Decode.Beta)
			perfData.PrefillParms.Gamma = float32(tunedResults.ServiceParms.Prefill.Gamma)
			perfData.PrefillParms.Delta = float32(tunedResults.ServiceParms.Prefill.Delta)

			logger.Log.Debugf("Model tuner results: model=%s, accelerator=%s, alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f, NIS=%.2f",
				modelName,
				accName,
				perfData.DecodeParms.Alpha,
				perfData.DecodeParms.Beta,
				perfData.PrefillParms.Gamma,
				perfData.PrefillParms.Delta,
				tunedResults.NIS,
			)

			return nil
		}
	}
	return fmt.Errorf("model %q with accelerator %q not found in system data", modelName, accName)
}

// updateVAStatusWithTunedParams updates VA status with tuner results
// Only updates if values have actually changed to avoid unnecessary API calls
func updateVAStatusWithTunedParams(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	model string,
	accelerator string,
	tunedResults *tune.TunedResults,
) error {
	// Check if we already have tuned data and if it matches the new values
	if va.Status.TunerPerfData != nil &&
		hasValidParams(va.Status.TunerPerfData.PerfParms.DecodeParms, va.Status.TunerPerfData.PerfParms.PrefillParms) {
		if tunedParamsMatch(va, model, accelerator, tunedResults) {
			logger.Log.Debugf("Tuned parameters unchanged for variant %s, skipping status update", va.Name)
			return nil
		}
	}

	// convert *mat.Dense to slice of string slices to store covariance matrix in VA status
	covMatrixStatus := denseMatrixToSliceOfStrings(tunedResults.Covariance)

	// Only set NIS if it's non-negative
	// Negative NIS indicates that values couldn't be computed
	nisString := ""
	if tunedResults.NIS >= 0 {
		nisString = fmt.Sprintf("%.6f", tunedResults.NIS)
	}

	va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{
		Model:       model,
		Accelerator: accelerator,
		UpdatedAt:   metav1.NewTime(time.Now()),
		PerfParms: llmdVariantAutoscalingV1alpha1.PerfParms{
			DecodeParms: map[string]string{
				"alpha": fmt.Sprintf("%.6f", tunedResults.ServiceParms.Decode.Alpha),
				"beta":  fmt.Sprintf("%.6f", tunedResults.ServiceParms.Decode.Beta),
			},
			PrefillParms: map[string]string{
				"gamma": fmt.Sprintf("%.6f", tunedResults.ServiceParms.Prefill.Gamma),
				"delta": fmt.Sprintf("%.6f", tunedResults.ServiceParms.Prefill.Delta),
			},
		},
		NIS:              nisString,
		CovarianceMatrix: covMatrixStatus,
	}

	logger.Log.Debugf("Updated tuner status for variant %s: alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f, NIS=%s",
		va.Name,
		tunedResults.ServiceParms.Decode.Alpha,
		tunedResults.ServiceParms.Decode.Beta,
		tunedResults.ServiceParms.Prefill.Gamma,
		tunedResults.ServiceParms.Prefill.Delta,
		nisString)

	return nil
}

// ensureTunerPerfDataInitialized ensures TunerPerfData and its parameter maps are initialized.
func ensureTunerPerfDataInitialized(va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling) {
	if va.Status.TunerPerfData == nil {
		va.Status.TunerPerfData = &llmdVariantAutoscalingV1alpha1.TunerPerfData{}
	}
	if va.Status.TunerPerfData.PerfParms.DecodeParms == nil {
		va.Status.TunerPerfData.PerfParms.DecodeParms = make(map[string]string)
	}
	if va.Status.TunerPerfData.PerfParms.PrefillParms == nil {
		va.Status.TunerPerfData.PerfParms.PrefillParms = make(map[string]string)
	}
}

// tunedParamsMatch checks if the new tuned results match existing status
func tunedParamsMatch(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	model string,
	accelerator string,
	tunedResults *tune.TunedResults,
) bool {
	if va.Status.TunerPerfData == nil {
		return false
	}

	existing := va.Status.TunerPerfData

	// Check model and accelerator
	if existing.Model != model || existing.Accelerator != accelerator {
		return false
	}

	// Compare performance parameters with epsilon for float comparison
	alpha, err := strconv.ParseFloat(existing.PerfParms.DecodeParms["alpha"], 64)
	if err != nil || !tune.FloatEqual(alpha, float64(tunedResults.ServiceParms.Decode.Alpha), tune.DefaultEpsilon) {
		return false
	}

	beta, err := strconv.ParseFloat(existing.PerfParms.DecodeParms["beta"], 64)
	if err != nil || !tune.FloatEqual(beta, float64(tunedResults.ServiceParms.Decode.Beta), tune.DefaultEpsilon) {
		return false
	}

	gamma, err := strconv.ParseFloat(existing.PerfParms.PrefillParms["gamma"], 64)
	if err != nil || !tune.FloatEqual(gamma, float64(tunedResults.ServiceParms.Prefill.Gamma), tune.DefaultEpsilon) {
		return false
	}

	delta, err := strconv.ParseFloat(existing.PerfParms.PrefillParms["delta"], 64)
	if err != nil || !tune.FloatEqual(delta, float64(tunedResults.ServiceParms.Prefill.Delta), tune.DefaultEpsilon) {
		return false
	}

	// Compare NIS
	nis, err := strconv.ParseFloat(existing.NIS, 64)
	if err != nil || !tune.FloatEqual(nis, tunedResults.NIS, tune.DefaultEpsilon) {
		return false
	}

	// Compare covariance matrix
	if !covarianceMatrixMatches(existing.CovarianceMatrix, tunedResults.Covariance) {
		return false
	}

	return true
}

// covarianceMatrixMatches compares stored matrix with new matrix
func covarianceMatrixMatches(stored [][]string, newMat *mat.Dense) bool {
	r, c := newMat.Dims()
	if len(stored) != r {
		return false
	}

	for i := range r {
		if len(stored[i]) != c {
			return false
		}
		for j := range c {
			storedVal, err := strconv.ParseFloat(stored[i][j], 64)
			if err != nil {
				return false
			}
			if !tune.FloatEqual(storedVal, newMat.At(i, j), tune.DefaultEpsilon) {
				return false
			}
		}
	}

	return true
}

func denseMatrixToSliceOfStrings(m *mat.Dense) [][]string {
	r, c := m.Dims()
	result := make([][]string, r)

	for i := range r {
		result[i] = make([]string, c)
		for j := range c {
			result[i][j] = fmt.Sprintf("%.6f", m.At(i, j))
		}
	}
	return result
}

func findServerInSystemData(systemData *infernoConfig.SystemData, serverName string) *infernoConfig.ServerSpec {
	for i := range systemData.Spec.Servers.Spec {
		if systemData.Spec.Servers.Spec[i].Name == serverName {
			return &systemData.Spec.Servers.Spec[i]
		}
	}
	return nil
}

// updateSystemDataWithState updates SystemData with the given state parameters
func updateSystemDataWithState(
	systemData *infernoConfig.SystemData,
	modelName, accName string,
	state []float64,
) error {
	if len(state) != 4 {
		return fmt.Errorf("invalid state length: expected 4, got %d", len(state))
	}

	for i := range systemData.Spec.Models.PerfData {
		perfData := &systemData.Spec.Models.PerfData[i]
		if perfData.Name == modelName && perfData.Acc == accName {
			perfData.DecodeParms.Alpha = float32(state[constants.StateIndexAlpha])
			perfData.DecodeParms.Beta = float32(state[constants.StateIndexBeta])
			perfData.PrefillParms.Gamma = float32(state[constants.StateIndexGamma])
			perfData.PrefillParms.Delta = float32(state[constants.StateIndexDelta])

			logger.Log.Debugf("Updated SystemData for model=%s, accelerator=%s: alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f",
				modelName,
				accName,
				perfData.DecodeParms.Alpha,
				perfData.DecodeParms.Beta,
				perfData.PrefillParms.Gamma,
				perfData.PrefillParms.Delta)

			return nil
		}
	}
	return fmt.Errorf("model %q with accelerator %q not found in system data", modelName, accName)
}

// updateVAStatusWithState updates VA status with the given state parameters.
// This is used when ActivateModelTuner=false but we need to populate status for potential future tuning.
func updateVAStatusWithState(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	model, accelerator string,
	state []float64,
) error {
	if len(state) != 4 {
		return fmt.Errorf("invalid state length: expected 4, got %d", len(state))
	}

	ensureTunerPerfDataInitialized(va)

	// Update parameters
	va.Status.TunerPerfData.Model = model
	va.Status.TunerPerfData.Accelerator = accelerator
	va.Status.TunerPerfData.PerfParms.DecodeParms["alpha"] = fmt.Sprintf("%.6f", state[constants.StateIndexAlpha])
	va.Status.TunerPerfData.PerfParms.DecodeParms["beta"] = fmt.Sprintf("%.6f", state[constants.StateIndexBeta])
	va.Status.TunerPerfData.PerfParms.PrefillParms["gamma"] = fmt.Sprintf("%.6f", state[constants.StateIndexGamma])
	va.Status.TunerPerfData.PerfParms.PrefillParms["delta"] = fmt.Sprintf("%.6f", state[constants.StateIndexDelta])

	// Clear covariance matrix and NIS since this is not from tuning
	va.Status.TunerPerfData.CovarianceMatrix = nil
	va.Status.TunerPerfData.NIS = ""

	logger.Log.Debugf("Updated VA status for variant %s/%s, model %s, accelerator %s: state=[%.6f, %.6f, %.6f, %.6f]",
		va.Name,
		va.Namespace,
		model,
		accelerator,
		state[constants.StateIndexAlpha],
		state[constants.StateIndexBeta],
		state[constants.StateIndexGamma],
		state[constants.StateIndexDelta])

	return nil
}
