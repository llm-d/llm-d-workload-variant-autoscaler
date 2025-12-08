package tuner

import (
	"errors"
	"fmt"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logger"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
	infernoConfig "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	tune "github.com/llm-d-incubation/workload-variant-autoscaler/pkg/tuner"
)

// TuneModelPerfParams manages performance parameters for all active VAs.
// It ensures SystemData is populated with appropriate parameters before model analysis.
// For VAs with activateModelTuner=false: uses status/spec/guessInitState fallback
// For VAs with activateModelTuner=true: runs tuner and falls back on failure
func TuneModelPerfParams(
	activeVAs []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	autoGuessInitialState bool,
) error {
	for i := range activeVAs {
		va := &activeVAs[i]

		// Find corresponding server in SystemData
		serverName := utils.FullName(va.Name, va.Namespace)
		server := findServerInSystemData(systemData, serverName)
		if server == nil {
			logger.Log.Warnf("Server not found in SystemData, skipping variant %s/%s, serverName %s",
				va.Name,
				va.Namespace,
				serverName)
			continue
		}

		if !va.Spec.ActivateModelTuner {
			// activateModelTuner is False: fill system data with fallback parameters
			if err := setFallbackParamsInSystemData(va, systemData, server, autoGuessInitialState); err != nil {
				logger.Log.Warnf("Failed to set fallback parameters for variant %s/%s: %v",
					va.Name, va.Namespace, err)
			}
			continue
		}

		// activateModelTuner is True: attempt tuning
		if err := runTuningWithFallback(va, systemData, server, autoGuessInitialState); err != nil {
			logger.Log.Warnf("Tuning failed for variant %s/%s: %v", va.Name, va.Namespace, err)
		}
	}

	return nil
}

// setFallbackParamsInSystemData updates SystemData with fallback parameters.
// Priority depends on whether we have actual tuned results and autoGuessInitialState flag:
// - If status has tuned results (params + covariance): use status
// - Otherwise, if autoGuessInitialState=true: guessInitState -> spec
// - Otherwise, if autoGuessInitialState=false: spec -> guessInitState
func setFallbackParamsInSystemData(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) error {
	var state []float64
	var err error

	// Track if we can use previous status params as a fallback to avoid overwriting covariance matrix
	usedStatusParams := false

	// Priority 1: If status has previous tuned results (params + covariance), use them
	if HasFullTunedResults(va) {
		state, err = extractStateFromVAStatus(va)
		if err != nil {
			logger.Log.Debugf("Failed to extract state from status for variant %s/%s: %v",
				va.Name, va.Namespace, err)
		} else {
			logger.Log.Infof("Using tuned parameters from status for variant %s/%s",
				va.Name, va.Namespace)
			usedStatusParams = true
		}
	}

	// Priority 2 & 3: Order depends on autoGuessInitialState flag
	if state == nil {
		state, err = getInitialStateWithFallback(systemData, server, autoGuessInitialState)
		if err != nil {
			logger.Log.Errorf("Failed to get initial state for variant %s/%s: %v", va.Name, va.Namespace, err)
			return fmt.Errorf("all fallback methods failed: %w", err)
		}
		logger.Log.Infof("Using fallback parameters for variant %s/%s: alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f",
			va.Name, va.Namespace, state[constants.StateIndexAlpha], state[constants.StateIndexBeta], state[constants.StateIndexGamma], state[constants.StateIndexDelta])
	}

	// Update SystemData with the obtained parameters
	if err := updateSystemDataWithState(systemData, server.Model, server.CurrentAlloc.Accelerator, state); err != nil {
		logger.Log.Errorf("Failed to update SystemData for variant %s/%s, model %s, accelerator %s with state [%.6f, %.6f, %.6f, %.6f]: %v",
			va.Name, va.Namespace, server.Model, server.CurrentAlloc.Accelerator,
			state[constants.StateIndexAlpha], state[constants.StateIndexBeta], state[constants.StateIndexGamma], state[constants.StateIndexDelta], err)
		return fmt.Errorf("failed to update SystemData: %w", err)
	}

	// Only update VA status if we didn't use previous status params, to preserve covariance matrix
	if !usedStatusParams {
		if err := updateVAStatusWithState(va, server.Model, server.CurrentAlloc.Accelerator, state); err != nil {
			logger.Log.Warnf("Failed to update VA status for variant %s/%s with state [%.6f, %.6f, %.6f, %.6f]: %v",
				va.Name, va.Namespace, state[constants.StateIndexAlpha], state[constants.StateIndexBeta], state[constants.StateIndexGamma], state[constants.StateIndexDelta], err)
		}
	}

	return nil
}

// runTuningWithFallback attempts to run the tuner and falls back on failure.
func runTuningWithFallback(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) error {
	// Attempt to tune the server
	tunedResults, err := tuneServer(va, systemData, server, autoGuessInitialState)

	// Handle failure
	if err != nil {
		// Check if failure is due to insufficient metrics for tuning
		if errors.Is(err, ErrorInsufficientMetrics) {
			logger.Log.Debugf("Skipping tuning for variant %s/%s due to insufficient load. Using fallback parameters.",
				va.Name, va.Namespace)
		} else {
			// Actual tuner failure
			logger.Log.Warnf("Tuner failed completely for variant %s/%s: %v. Using fallback parameters.",
				va.Name, va.Namespace, err)
		}
		return setFallbackParamsInSystemData(va, systemData, server, autoGuessInitialState)
	}

	// Check if this is a validation failure (previous state returned)
	if tunedResults.ValidationFailed {
		// NIS validation failed, but we have previous state to keep
		logger.Log.Infof("Keeping previous tuned parameters for variant %s/%s due to NIS validation failure (NIS=%.6f)",
			va.Name, va.Namespace, tunedResults.NIS)

		if err := updateModelPerfDataInSystemData(systemData, server.Model, server.CurrentAlloc.Accelerator, tunedResults); err != nil {
			logger.Log.Warnf("Failed to update SystemData with previous params for variant %s/%s: %v",
				va.Name, va.Namespace, err)
			return setFallbackParamsInSystemData(va, systemData, server, autoGuessInitialState)
		}

		if err := updateVAStatusWithTunedParams(va, server.Model, server.CurrentAlloc.Accelerator, tunedResults); err != nil {
			logger.Log.Warnf("Failed to update VA status with previous params for variant %s/%s: %v",
				va.Name, va.Namespace, err)
		}

		return nil
	}

	// Tuning succeeded - update SystemData and VA status with new parameters
	if err := updateModelPerfDataInSystemData(systemData, server.Model, server.CurrentAlloc.Accelerator, tunedResults); err != nil {
		logger.Log.Warnf("Failed to update SystemData with tuned params for variant %s/%s: %v",
			va.Name, va.Namespace, err)
		return setFallbackParamsInSystemData(va, systemData, server, autoGuessInitialState)
	}

	if err := updateVAStatusWithTunedParams(va, server.Model, server.CurrentAlloc.Accelerator, tunedResults); err != nil {
		logger.Log.Warnf("Failed to update VA status with tuned params for variant %s/%s: %v",
			va.Name, va.Namespace, err)
	}

	logger.Log.Infof("Tuned performance parameters result: variant %s/%s - alpha: %.6f, beta: %.6f, gamma: %.6f, delta: %.6f",
		va.Name,
		va.Namespace,
		tunedResults.ServiceParms.Decode.Alpha,
		tunedResults.ServiceParms.Decode.Beta,
		tunedResults.ServiceParms.Prefill.Gamma,
		tunedResults.ServiceParms.Prefill.Delta)

	return nil
}

// tuneServer tunes parameters for a single server
func tuneServer(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) (*tune.TunedResults, error) {
	// Check if we should skip tuning due to transient delay
	// TODO: Do we need to skip tuning based on timing

	// Create tuner for this server
	tuner, err := createTuner(va, systemData, server, autoGuessInitialState)
	if err != nil {
		// Check if failure is due to insufficient metrics for tuning
		if errors.Is(err, ErrorInsufficientMetrics) {
			logger.Log.Debugf("Skipping tuning for variant %s/%s due to insufficient load. Using fallback parameters.",
				va.Name, va.Namespace)
		} else {
			// Actual tuner failure
			logger.Log.Warnf("Tuner failed completely for variant %s/%s: %v. Using fallback parameters.",
				va.Name, va.Namespace, err)
		}
		return nil, err
	}

	// Convert server's CurrentAlloc to Environment
	env, err := convertAllocToEnvironment(server.CurrentAlloc)
	if err != nil {
		return nil, fmt.Errorf("failed to convert allocation to environment: %w", err)
	}

	// Update environment with latest metrics
	err = tuner.UpdateEnvironment(env)
	if err != nil {
		return nil, fmt.Errorf("failed to update environment for server %s: %w", server.Name, err)
	}

	// Run Kalman filter (predict + update)
	tunedResults, err := tuner.Run()
	if err != nil {
		// Complete failure - tuner couldn't run at all
		return nil, fmt.Errorf("tuner execution failed completely: %w", err)
	}

	// Check if validation failed (NIS threshold exceeded)
	if tunedResults.ValidationFailed {
		// NIS validation failed, but we have previous state to use
		logger.Log.Warnf("Tuner NIS validation failed for variant %s/%s, server %s (NIS=%.2f exceeds threshold %.2f) - Keeping previous state: alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f",
			va.Name,
			va.Namespace,
			server.Name,
			tunedResults.NIS,
			constants.DefaultMaxNIS,
			tunedResults.ServiceParms.Decode.Alpha,
			tunedResults.ServiceParms.Decode.Beta,
			tunedResults.ServiceParms.Prefill.Gamma,
			tunedResults.ServiceParms.Prefill.Delta)

		return tunedResults, nil // Return the previous state
	}

	// Valid update - NIS validation passed
	logger.Log.Infof("Tuner validation succeeded for variant %s/%s, server %s - New NIS: %.6f",
		va.Name,
		va.Namespace,
		server.Name,
		tunedResults.NIS)

	return tunedResults, nil
}

func createTuner(
	va *llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	systemData *infernoConfig.SystemData,
	server *infernoConfig.ServerSpec,
	autoGuessInitialState bool,
) (*tune.Tuner, error) {
	/*
			if the tuner doesn't exist then create a new tuner

		    ------------------------- COMMENTS --------------------------------
			Onboarding a new tuner requires initializing the Kalman Filter.
			To facilitate the initialization, we require mean value of observations (expected observations)
			on performance metrics and a current state if available, otherwise an initial state.
			The current design choice is that the current state is read from the VA status.
			If no current state is available, the filter starts with the initial state provided in the model profile field VA spec.
			This initial state is derived from offline benchmarking.
			On the other hand, it is obvious that the SLO expectation should dictate expected observations.
			------------------------------------------------------------------
	*/

	// Get state params and covariance matrix from VA status (if exists), otherwise return only the state params from VA spec
	state, covMatrix, err := getStateAndCovariance(va, systemData, server, autoGuessInitialState)
	if err != nil {
		return nil, fmt.Errorf("failed to get state values from the VA")
	}

	// Find SLO targets from system data
	slos, err := findSLOInSystemData(systemData, server.Model, server.Class)
	if err != nil {
		return nil, fmt.Errorf("failed to find SLO for model class pair %s, %s: %w",
			server.Model, server.Class, err)
	}

	// Log the tuner configuration being used
	hasCov := len(covMatrix) > 0
	logger.Log.Debugf("[Tuner Config] variant=%s/%s | Initial state: alpha=%.6f, beta=%.6f, gamma=%.6f, delta=%.6f | SLO targets: TTFT=%.2fms, ITL=%.2fms | Has covariance=%v",
		va.Name,
		va.Namespace,
		state[constants.StateIndexAlpha],
		state[constants.StateIndexBeta],
		state[constants.StateIndexGamma],
		state[constants.StateIndexDelta],
		slos[0], // TTFT SLO
		slos[1], // ITL SLO
		hasCov)

	// build tuner config from initial state and slo
	configData, err := BuildTunerConfig(state, covMatrix, slos)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	env, err := convertAllocToEnvironment(server.CurrentAlloc)
	if err != nil {
		return nil, fmt.Errorf("failed to convert allocation to environment: %w", err)
	}

	if !env.Valid() {
		// Invalid environment is typically due to no traffic observed
		return nil, ErrorInsufficientMetrics
	}

	// create tuner
	tuner, err := tune.NewTuner(configData, env)
	if err != nil {
		return nil, fmt.Errorf("failed to create tuner: %w", err)
	}

	return tuner, nil
}
