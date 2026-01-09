/*
Copyright 2025 The llm-d Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package saturation

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	actuator "github.com/llm-d-incubation/workload-variant-autoscaler/internal/actuator"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	collectorv2 "github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector/v2"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/common"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/executor"
	saturationmetrics "github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/saturation/metrics"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

type Engine struct {
	client   client.Client
	scheme   *runtime.Scheme
	executor executor.Executor

	Recorder record.EventRecorder

	// ReplicaMetricsCollectorV2 is the v2 collector for replica metrics
	ReplicaMetricsCollectorV2 *saturationmetrics.ReplicaMetricsCollector
}

// getVariantKey returns a unique key for a variant combining namespace and name.
// This ensures no collisions when multiple namespaces have deployments with the same name.
func getVariantKey(namespace, name string) string {
	return namespace + "/" + name
}

// NewEngine creates a new instance of the saturation engine.
func NewEngine(client client.Client, scheme *runtime.Scheme, recorder record.EventRecorder, metricsRegistry *collectorv2.SourceRegistry) *Engine {
	promSource := metricsRegistry.Get("prometheus") // assume prometheus source is registered

	engine := Engine{
		client:                    client,
		scheme:                    scheme,
		Recorder:                  recorder,
		ReplicaMetricsCollectorV2: saturationmetrics.NewReplicaMetricsCollector(promSource, client),
	}

	engine.executor = executor.NewPollingExecutor(executor.PollingConfig{
		Config: executor.Config{
			OptimizeFunc: engine.optimize,
		},
		Interval:     30 * time.Second,
		RetryBackoff: 100 * time.Millisecond,
	})

	// Register saturation-specific queries in the metrics registry
	saturationmetrics.RegisterSaturationQueries(metricsRegistry)

	return &engine
}

// StartOptimizeLoop starts the optimization loop for the saturation engine.
// It runs until the context is cancelled.
func (e *Engine) StartOptimizeLoop(ctx context.Context) {
	e.executor.Start(ctx)
}

// optimize performs the optimization logic.
func (e *Engine) optimize(ctx context.Context) error {
	logger := ctrl.LoggerFrom(ctx)

	//TODO: move interval to manager.yaml
	interval := common.Config.GetOptimizationInterval()

	// Update the executor interval if changed
	// Note: simple polling executor might not support dynamic interval update easily without restart,
	// but here we just check it. The original code used RequeueAfter.
	// The PollingExecutor uses fixed interval.
	// TODO: Support dynamic interval in Executor if needed. For now, we log and proceed.
	if interval != "" {
		if dur, err := time.ParseDuration(interval); err == nil {
			// e.executor.SetInterval(dur) // If supported
			_ = dur
		}
	}

	if strings.EqualFold(os.Getenv("WVA_SCALE_TO_ZERO"), "true") {
		logger.Info("Scaling to zero is enabled")
	}

	activeVAs, err := utils.ActiveVariantAutoscaling(ctx, e.client)
	if err != nil {
		logger.Error(err, "Unable to get active variant autoscalings")
		return err
	}

	if len(activeVAs) == 0 {
		logger.Info("No active VariantAutoscalings found, skipping optimization")
		return nil
	}

	// Collected accelerator inventory (only in limited mode)
	if strings.EqualFold(os.Getenv("WVA_LIMITED_MODE"), "true") {
		inventory, err := collector.CollectInventoryK8S(ctx, e.client)
		if err != nil {
			logger.Error(err, "Failed to collect cluster inventory")
			// do not proceed to optimization if inventory collection fails in limited mode
			return err
		}
		// always print inventory until optimizer consumes it
		logger.Info("Collected cluster accelerator inventory (Limited Mode)", "inventory", inventory)
	}

	saturationConfigMap := common.Config.GetSaturationConfig()
	if len(saturationConfigMap) == 0 {
		logger.Info("Saturation scaling config not loaded yet, skipping optimization")
		return nil
	}

	saturationConfig, ok := saturationConfigMap["default"]
	if !ok {
		logger.Info("Default saturation scaling config not found, skipping optimization")
		return nil
	}

	// Group VAs by model for per-model capacity analysis
	modelGroups := utils.GroupVariantAutoscalingByModel(activeVAs)
	logger.Info("Grouped VAs by model",
		"modelCount", len(modelGroups),
		"totalVAs", len(activeVAs))

	// Process each model independently
	allDecisions := make([]interfaces.VariantDecision, 0)

	// Create VA lookup map for applySaturationDecisions (used to access VA status and update decisions)
	// Copy slice elements to local variable to ensure stable pointers
	// Use namespace/deploymentName as key to avoid collisions when multiple namespaces have same deployment name
	vaMap := make(map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling, len(activeVAs))
	for i := range activeVAs {
		va := activeVAs[i] // Copy to local variable to ensure stable pointer
		vaMap[getVariantKey(va.Namespace, va.GetScaleTargetName())] = &va
	}

	// Create map to store current allocations populated during metrics collection
	// Keyed by deployment name (ScaleTargetName)
	currentAllocations := make(map[string]*interfaces.Allocation)

	for groupKey, modelVAs := range modelGroups {
		// The groupKey is "modelID|namespace" - extract actual modelID from VAs
		// All VAs in the group have the same modelID and namespace
		modelID := modelVAs[0].Spec.ModelID
		logger.Info("Processing model",
			"modelID", modelID,
			"namespace", modelVAs[0].Namespace,
			"variantCount", len(modelVAs),
			"groupKey", groupKey)

		saturationTargets, saturationAnalysis, variantStates, err := e.RunSaturationAnalysis(ctx, modelID, modelVAs, saturationConfig, e.client)
		if err != nil {
			logger.Error(err, "Saturation analysis failed",
				"modelID", modelID)

			// Activate safety net to ensure HPA doesn't scale to zero on partial failure
			e.emitSafetyNetMetrics(ctx, modelVAs, currentAllocations)
			continue
		}

		var finalDecisions []interfaces.VariantDecision
		if saturationAnalysis != nil {
			finalDecisions = e.convertSaturationTargetsToDecisions(ctx, saturationTargets, saturationAnalysis, variantStates)
			logger.Info("Saturation-only decisions made for model",
				"modelID", modelID,
				"decisionCount", len(finalDecisions))
			allDecisions = append(allDecisions, finalDecisions...)
		} else {
			// If saturationAnalysis is nil (e.g. no metrics), we just skip this model
			logger.V(logging.DEBUG).Info("Skipping decision application for model: saturation analysis is nil (likely no metrics)",
				"modelID", modelID)
		}
	}

	// STEP 3: Apply decisions and update VA status
	// Always call applySaturationDecisions, even with empty decisions.
	// This function also updates VA.Status.CurrentAlloc with collected metrics
	// and emits HPA metrics, which must happen every reconciliation cycle.
	if len(allDecisions) > 0 {
		logger.Info("Applying scaling decisions",
			"totalDecisions", len(allDecisions))
	} else {
		logger.Info("No scaling decisions to apply, updating VA status with metrics")
	}
	if err := e.applySaturationDecisions(ctx, allDecisions, vaMap, currentAllocations); err != nil {
		logger.Error(err, "Failed to apply saturation decisions")
		return err
	}

	logger.Info("Optimization completed successfully",
		"mode", "saturation-only",
		"modelsProcessed", len(modelGroups),
		"decisionsApplied", len(allDecisions))

	return nil
}

// BuildVariantStates extracts current and desired replica counts from VAs for capacity analysis.
func (e *Engine) BuildVariantStates(
	ctx context.Context,
	vas []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	deployments map[string]*appsv1.Deployment,
	k8sClient client.Client,
) []interfaces.VariantReplicaState {
	states := make([]interfaces.VariantReplicaState, 0, len(vas))

	for _, va := range vas {
		// Get current replicas from deployment using ScaleTargetRef
		// Get current replicas from deployment using ScaleTargetRef
		var deploy *appsv1.Deployment
		var found bool

		// Try to look up in provided map first (optimization)
		if deployments != nil {
			// Deployment map is keyed by deployment name
			// But do we know the deployment name?
			// va.GetScaleTargetName() gives the name.
			deploy, found = deployments[va.GetScaleTargetName()]
		}

		if !found {
			// Fallback to API call
			fetchedDeploy := &appsv1.Deployment{}
			if err := utils.GetDeploymentWithBackoff(ctx, k8sClient, va.GetScaleTargetName(), va.Namespace, fetchedDeploy); err != nil {
				ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Could not get deployment for VA, skipping",
					"variant", va.Name,
					"error", err)
				continue
			}
			deploy = fetchedDeploy
			ctrl.LoggerFrom(ctx).V(1).Info("BuildVariantStates fallback lookup", "variant", va.Name, "deployName", deploy.Name, "specReplicas", deploy.Spec.Replicas, "statusReplicas", deploy.Status.Replicas, "readyReplicas", deploy.Status.ReadyReplicas)
		} else {
			ctrl.LoggerFrom(ctx).V(1).Info("BuildVariantStates map lookup", "variant", va.Name, "deployName", deploy.Name, "specReplicas", deploy.Spec.Replicas, "statusReplicas", deploy.Status.Replicas, "readyReplicas", deploy.Status.ReadyReplicas)
		}

		currentReplicas := int(deploy.Status.Replicas)
		if currentReplicas == 0 && deploy.Spec.Replicas != nil {
			currentReplicas = int(*deploy.Spec.Replicas)
		}

		// Calculate pending replicas (not yet ready)
		readyReplicas := int(deploy.Status.ReadyReplicas)
		pendingReplicas := currentReplicas - readyReplicas
		if pendingReplicas < 0 {
			// This indicates an unexpected state where readyReplicas exceeds currentReplicas.
			// Log at Info level since this inconsistency should be visible to operators.
			ctrl.LoggerFrom(ctx).Info("Unexpected state: readyReplicas exceeds currentReplicas, clamping pendingReplicas to 0",
				"variant", va.Name, "currentReplicas", currentReplicas, "readyReplicas", readyReplicas)
			pendingReplicas = 0
		}

		ctrl.LoggerFrom(ctx).V(1).Info("BuildVariantStates result", "variant", va.Name, "currentReplicas", currentReplicas, "readyReplicas", readyReplicas, "pendingReplicas", pendingReplicas)

		states = append(states, interfaces.VariantReplicaState{
			VariantName:     deploy.Name,
			CurrentReplicas: currentReplicas,
			DesiredReplicas: va.Status.DesiredOptimizedAlloc.NumReplicas,
			PendingReplicas: pendingReplicas,
		})
	}

	return states
}

// convertSaturationTargetsToDecisions converts saturation-only targets to VariantDecisions.
// Used when model-based optimizer is disabled (saturation-only mode).
func (e *Engine) convertSaturationTargetsToDecisions(
	ctx context.Context,
	saturationTargets map[string]int,
	saturationAnalysis *interfaces.ModelSaturationAnalysis,
	variantStates []interfaces.VariantReplicaState,
) []interfaces.VariantDecision {
	logger := ctrl.LoggerFrom(ctx)
	decisions := make([]interfaces.VariantDecision, 0, len(saturationTargets))

	// Build variant analysis map for quick lookup
	vaMap := make(map[string]*interfaces.VariantSaturationAnalysis)
	for i := range saturationAnalysis.VariantAnalyses {
		va := &saturationAnalysis.VariantAnalyses[i]
		vaMap[va.VariantName] = va
	}

	// Build state map for quick lookup
	stateMap := make(map[string]interfaces.VariantReplicaState)
	for _, state := range variantStates {
		stateMap[state.VariantName] = state
	}

	for variantName, targetReplicas := range saturationTargets {
		state := stateMap[variantName]
		va := vaMap[variantName]

		var action interfaces.SaturationAction
		if targetReplicas > state.CurrentReplicas {
			action = interfaces.ActionScaleUp
		} else if targetReplicas < state.CurrentReplicas {
			action = interfaces.ActionScaleDown
		} else {
			action = interfaces.ActionNoChange
		}

		decision := interfaces.VariantDecision{
			VariantName:        variantName,
			Namespace:          saturationAnalysis.Namespace,
			ModelID:            saturationAnalysis.ModelID,
			CurrentReplicas:    state.CurrentReplicas,
			TargetReplicas:     targetReplicas,
			DesiredReplicas:    state.DesiredReplicas,
			Action:             action,
			SaturationBased:    true,
			SaturationOnly:     true,
			ModelBasedDecision: false,
			SafetyOverride:     false,
			Reason:             "saturation-only mode: " + string(action),
		}

		if va != nil {
			decision.AcceleratorName = va.AcceleratorName
			decision.Cost = va.Cost
		} else {
			logger.Info("No variant analysis found for decision (metrics may be unavailable)",
				"variant", variantName)
		}

		decisions = append(decisions, decision)
	}

	return decisions
}

// RunSaturationAnalysis performs saturation analysis for a model and returns Saturation targets.
func (e *Engine) RunSaturationAnalysis(
	ctx context.Context,
	modelID string,
	modelVAs []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	SaturationConfig interfaces.SaturationScalingConfig,
	k8sClient client.Client,
) (map[string]int, *interfaces.ModelSaturationAnalysis, []interfaces.VariantReplicaState, error) {
	if len(modelVAs) == 0 {
		return nil, nil, nil, fmt.Errorf("no VAs provided for model %s", modelID)
	}

	logger := ctrl.LoggerFrom(ctx)
	namespace := modelVAs[0].Namespace // All VAs of same model are in same namespace

	// Build variant costs map, deployments map, and VAs map for metrics collection
	variantCosts := make(map[string]float64)
	deployments := make(map[string]*appsv1.Deployment)
	variantAutoscalings := make(map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)

	for i := range modelVAs {
		va := &modelVAs[i]

		// Get the deployment for this VA using ScaleTargetRef
		var deploy appsv1.Deployment
		err := utils.GetDeploymentWithBackoff(ctx, k8sClient, va.GetScaleTargetName(), va.Namespace, &deploy)
		if err != nil {
			logger.V(logging.DEBUG).Info("Could not get deployment for VA",
				"variant", va.Name,
				"deployment", va.GetScaleTargetName(),
				"error", err)
			continue
		}

		// Parse variant cost
		cost := saturation.DefaultVariantCost // default
		if va.Spec.VariantCost != "" {
			if parsedCost, err := strconv.ParseFloat(va.Spec.VariantCost, 64); err == nil {
				cost = parsedCost
			}
		}

		// Use deployment name as key (not VA name) since getExistingPods uses
		// the key to build pod name regex filters for Prometheus queries
		deployments[deploy.Name] = &deploy
		variantAutoscalings[deploy.Name] = va
		variantCosts[deploy.Name] = cost
	}

	// Collect Saturation metrics using v2 collector
	logger.V(logging.DEBUG).Info("Using v2 collector for replica metrics",
		"modelID", modelID,
		"namespace", namespace)
	replicaMetrics, err := e.ReplicaMetricsCollectorV2.CollectReplicaMetrics(ctx, modelID, namespace, deployments, variantAutoscalings, variantCosts)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to collect Saturation metrics for model %s: %w", modelID, err)
	}

	logger.V(logging.DEBUG).Info("Collected saturation metrics",
		"modelID", modelID,
		"namespace", namespace,
		"metricsCount", len(replicaMetrics))

	// If no metrics available, skip saturation analysis entirely
	// This prevents creating invalid decisions when pods are not ready or metrics are unavailable
	if len(replicaMetrics) == 0 {
		logger.Info("No saturation metrics available for model, skipping analysis",
			"modelID", modelID,
			"namespace", namespace)
		return nil, nil, nil, nil // Return nil to signal skip due to metrics unavailable, not error
	}

	// Analyze saturation across all variants
	saturationAnalyzer := saturation.NewAnalyzer()
	saturationAnalysis, err := saturationAnalyzer.AnalyzeModelSaturation(ctx, modelID, namespace, replicaMetrics, SaturationConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to analyze Saturation for model %s: %w", modelID, err)
	}

	logger.Info("Saturation analysis completed",
		"modelID", modelID,
		"totalReplicas", saturationAnalysis.TotalReplicas,
		"nonSaturated", saturationAnalysis.NonSaturatedCount,
		"shouldScaleUp", saturationAnalysis.ShouldScaleUp,
		"scaleDownSafe", saturationAnalysis.ScaleDownSafe)

	// Build variant states (current and desired replicas)
	variantStates := e.BuildVariantStates(ctx, modelVAs, deployments, k8sClient)

	// Calculate saturation-based targets
	saturationTargets := saturationAnalyzer.CalculateSaturationTargets(ctx, saturationAnalysis, variantStates)

	logger.V(logging.DEBUG).Info("Saturation targets calculated",
		"modelID", modelID,
		"targets", saturationTargets)

	return saturationTargets, saturationAnalysis, variantStates, nil
}

// applySaturationDecisions updates VA status and emits metrics based on Saturation decisions.
func (e *Engine) applySaturationDecisions(
	ctx context.Context,
	decisions []interfaces.VariantDecision,
	vaMap map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	currentAllocations map[string]*interfaces.Allocation,
) error {
	logger := ctrl.LoggerFrom(ctx)
	// Create a map of decisions for O(1) lookup
	// Use namespace/variantName as key to match vaMap and avoid collisions
	decisionMap := make(map[string]interfaces.VariantDecision)
	for _, d := range decisions {
		decisionMap[getVariantKey(d.Namespace, d.VariantName)] = d
	}

	// Iterate over ALL active VAs to ensure we update status and trigger reconciliation for everyone
	for vaName, va := range vaMap {
		decision, hasDecision := decisionMap[vaName]

		if hasDecision {
			logger.Info("Processing decision for VA",
				"variant", vaName,
				"action", decision.Action,
				"current", decision.CurrentReplicas,
				"target", decision.TargetReplicas)
		} else {
			logger.V(logging.DEBUG).Info("No scaling decision for VA, but updating status to trigger reconcile",
				"variant", vaName)
		}

		// Fetch latest version from API server to avoid conflicts
		var updateVa llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		if err := utils.GetVariantAutoscalingWithBackoff(ctx, e.client, va.Name, va.Namespace, &updateVa); err != nil {
			logger.Error(err, "Failed to get latest VA from API server",
				"name", va.Name)
			continue
		}

		// Update CurrentAlloc from local analysis (which has the latest metrics)
		// We use currentAllocations map instead of Status.CurrentAlloc
		if currentAlloc, ok := currentAllocations[vaName]; ok {
			// If we have a decision, attach current alloc to it for cache
			// If we have a decision, attach current alloc to it for cache
			// (Future logic if needed)
			_ = currentAlloc // Used for something?
			// Previously we updated va.Status.CurrentAlloc = currentAlloc
			// Now we just don't update status with it.
		}

		// Copy MetricsAvailable condition from local analysis (set during metrics collection)
		// This condition was set on `va` but we fetched a fresh `updateVa` from API server
		if metricsCondition := llmdVariantAutoscalingV1alpha1.GetCondition(va, llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable); metricsCondition != nil {
			llmdVariantAutoscalingV1alpha1.SetCondition(&updateVa,
				llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable,
				metricsCondition.Status,
				metricsCondition.Reason,
				metricsCondition.Message)
		}

		// Determine target replicas and accelerator
		var targetReplicas int
		var acceleratorName string
		var reason string

		if hasDecision {
			targetReplicas = decision.TargetReplicas
			acceleratorName = decision.AcceleratorName
			reason = decision.Reason
		} else {
			// No change/decision: Keep current target or default to current replicas
			// We effectively explicitly "decide" to keep things as they are if no decision was made
			if updateVa.Status.DesiredOptimizedAlloc.NumReplicas > 0 {
				targetReplicas = updateVa.Status.DesiredOptimizedAlloc.NumReplicas
			} else if curr, ok := currentAllocations[vaName]; ok {
				targetReplicas = curr.NumReplicas
			}
			// Keep existing accelerator or use current
			if updateVa.Status.DesiredOptimizedAlloc.Accelerator != "" {
				acceleratorName = updateVa.Status.DesiredOptimizedAlloc.Accelerator
			} else if curr, ok := currentAllocations[vaName]; ok {
				acceleratorName = curr.Accelerator
			}
			reason = "No scaling decision (optimization loop)"
		}

		// If we still don't have an accelerator name (e.g. new VA, no decision, no current alloc), we can't update status sensibly
		if acceleratorName == "" {
			logger.Info("Skipping status update for VA without accelerator info",
				"variant", vaName)
			continue
		}

		// Update DesiredOptimizedAlloc
		// ALWAYS update LastRunTime to trigger reconciliation in the controller
		updateVa.Status.DesiredOptimizedAlloc = llmdVariantAutoscalingV1alpha1.OptimizedAlloc{
			NumReplicas: targetReplicas,
			Accelerator: acceleratorName,
			LastRunTime: metav1.Now(),
		}
		updateVa.Status.Actuation.Applied = false // Reset applied status until Actuator handles it (if needed)

		// Set condition based on decision characteristics (or lack thereof)
		if hasDecision {
			if decision.SafetyOverride {
				llmdVariantAutoscalingV1alpha1.SetCondition(&updateVa,
					llmdVariantAutoscalingV1alpha1.TypeOptimizationReady,
					metav1.ConditionTrue,
					"SaturationSafetyOverride",
					fmt.Sprintf("saturation safety override: %s", reason))
			} else if decision.SaturationOnly {
				llmdVariantAutoscalingV1alpha1.SetCondition(&updateVa,
					llmdVariantAutoscalingV1alpha1.TypeOptimizationReady,
					metav1.ConditionTrue,
					"SaturationOnlyMode",
					fmt.Sprintf("saturation-only decision: %s (target: %d replicas)", reason, targetReplicas))
			} else {
				llmdVariantAutoscalingV1alpha1.SetCondition(&updateVa,
					llmdVariantAutoscalingV1alpha1.TypeOptimizationReady,
					metav1.ConditionTrue,
					llmdVariantAutoscalingV1alpha1.ReasonOptimizationSucceeded,
					fmt.Sprintf("Hybrid mode: %s (target: %d replicas)", reason, targetReplicas))
			}
		} else {
			// No active decision (just refreshing)
			llmdVariantAutoscalingV1alpha1.SetCondition(&updateVa,
				llmdVariantAutoscalingV1alpha1.TypeOptimizationReady,
				metav1.ConditionTrue,
				llmdVariantAutoscalingV1alpha1.ReasonOptimizationSucceeded,
				"Optimization loop ran (no scaling change needed)")
		}

		// Emit metrics for external autoscalers (Important: Actuator emits these)
		// We should emit metrics even if no decision changed, to keep HPA alive
		act := actuator.NewActuator(e.client)
		/*
		   NOTE: emitSafetyNetMetrics handles cases where optimization FAILS.
		   Here we are in the success path (optimization ran, even if no change).
		   We should ensure metrics are emitted for the External Scaler.
		*/

		// Ensure we have a valid SAT/Model decision "SaturationOnly" flag for metric emission context if needed
		// For now we assume if no decision, it's not saturation-only forced override, just normal op.
		// isSaturationOnly := false
		// if hasDecision {
		// 	isSaturationOnly = decision.SaturationOnly
		// }

		if err := act.EmitMetrics(ctx, &updateVa); err != nil {
			logger.Error(err, "Failed to emit metrics for external autoscalers",
				"variant", updateVa.Name)
		} else {
			// Only log detail if we had a decision or periodically (to avoid spamming logs on every loop for no-ops)
			if hasDecision {
				logger.Info("Successfully emitted metrics",
					"variant", updateVa.Name,
					"target", targetReplicas,
					"accelerator", acceleratorName)
			}
			updateVa.Status.Actuation.Applied = true
		}

		// Update Shared State and Trigger Reconcile via Channel
		// This avoids any API server interaction from the Engine.

		// 1. Update Cache
		common.DecisionCache.Set(va.Name, va.Namespace, interfaces.VariantDecision{
			VariantName:       vaName,
			Namespace:         va.Namespace,
			TargetReplicas:    targetReplicas,
			AcceleratorName:   acceleratorName,
			LastRunTime:       metav1.Now(),
			CurrentAllocation: currentAllocations[vaName],
			// Pass other fields if needed, but these are crucial for Status
		})

		// 2. Trigger Reconciler
		common.DecisionTrigger <- event.GenericEvent{
			Object: &updateVa,
		}

		if hasDecision {
			logger.Info("Applied saturation decision via shared cache",
				"variant", vaName,
				"action", decision.Action,
				"target", targetReplicas,
				"reason", reason)
		}
	}

	return nil
}

// emitSafetyNetMetrics emits fallback metrics when saturation analysis fails.
func (e *Engine) emitSafetyNetMetrics(
	ctx context.Context,
	modelVAs []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	currentAllocations map[string]*interfaces.Allocation,
) {
	logger := ctrl.LoggerFrom(ctx)
	act := actuator.NewActuator(e.client)

	for _, va := range modelVAs {
		// Determine desired replicas
		var desiredReplicas int32
		var fallbackSource string

		// Get current replicas for metric emission
		currentReplicas, err := act.GetCurrentDeploymentReplicas(ctx, &va)
		if err != nil {
			logger.Error(err, "Safety net: failed to get current replicas from Deployment for metrics", "using cached allocation",
				"variant", va.Name)
			if curr, ok := currentAllocations[va.GetScaleTargetName()]; ok {
				currentReplicas = int32(curr.NumReplicas)
			}
		}

		// Strategy 1: Use previous desired replicas if available
		if va.Status.DesiredOptimizedAlloc.NumReplicas > 0 {
			desiredReplicas = int32(va.Status.DesiredOptimizedAlloc.NumReplicas)
			fallbackSource = "previous-desired"
		} else {
			desiredReplicas = currentReplicas
			fallbackSource = "current-replicas"
		}

		// Determine accelerator - try status first, then labels, skip if unavailable
		// TODO: remove this checks when we will move to a new version of the CRD
		// with required accelerator field
		accelerator := va.Status.DesiredOptimizedAlloc.Accelerator
		if accelerator == "" {
			if curr, ok := currentAllocations[va.GetScaleTargetName()]; ok {
				accelerator = curr.Accelerator
			}
		}
		if accelerator == "" {
			// Try to get from VA labels as last resort
			if val, ok := va.Labels["inference.optimization/acceleratorName"]; ok && val != "" {
				accelerator = val
			}
		}
		if accelerator == "" {
			logger.Info("Safety net: skipping metric emission - no accelerator name available",
				"variant", va.Name)
			continue
		}

		// Emit safety net metrics
		if err := act.MetricsEmitter.EmitReplicaMetrics(
			ctx,
			&va,
			currentReplicas,
			desiredReplicas,
			accelerator,
		); err != nil {
			logger.Error(err, "Safety net: failed to emit metrics",
				"variant", va.Name)
			continue
		}

		logger.Info("Safety net activated: emitted fallback metrics",
			"variant", va.Name,
			"currentReplicas", currentReplicas,
			"desiredReplicas", desiredReplicas,
			"accelerator", accelerator,
			"fallbackSource", fallbackSource)
	}
}
