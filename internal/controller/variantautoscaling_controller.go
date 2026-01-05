/*
Copyright 2025.

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

package controller

import (
	"context"
	"fmt"
	"os"
	"strconv"

	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	yaml "gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	llmdVariantAutoscalingV1alpha1 "github.com/llm-d-incubation/workload-variant-autoscaler/api/v1alpha1"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/engines/common"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/saturation"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

// VariantAutoscalingReconciler reconciles a variantAutoscaling object
type VariantAutoscalingReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Recorder record.EventRecorder

	PromAPI promv1.API

	// MetricsCollector is the interface for collecting metrics from various backends
	// Defaults to Prometheus collector, but can be swapped for other backends (e.g., EPP)
	MetricsCollector interfaces.MetricsCollector
}

// +kubebuilder:rbac:groups=llmd.ai,resources=variantautoscalings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=llmd.ai,resources=variantautoscalings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=llmd.ai,resources=variantautoscalings/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes/status,verbs=get;list;update;patch;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;update;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

const (
	defaultConfigMapName = "workload-variant-autoscaler-variantautoscaling-config"
	// ServiceMonitor constants for watching controller's own metrics ServiceMonitor
	defaultServiceMonitorName = "workload-variant-autoscaler-controller-manager-metrics-monitor"

	defaultSaturationConfigMapName = "saturation-scaling-config"
)

func getNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "workload-variant-autoscaler-system"
}

func getConfigMapName() string {
	if name := os.Getenv("CONFIG_MAP_NAME"); name != "" {
		return name
	}
	return defaultConfigMapName
}

func getSaturationConfigMapName() string {
	if name := os.Getenv("SATURATION_CONFIG_MAP_NAME"); name != "" {
		return name
	}
	return defaultSaturationConfigMapName
}

var (
	// ServiceMonitor GVK for watching controller's own metrics ServiceMonitor
	serviceMonitorGVK = schema.GroupVersionKind{
		Group:   "monitoring.coreos.com",
		Version: "v1",
		Kind:    "ServiceMonitor",
	}
	configMapNamespace = getNamespace()
)

func (r *VariantAutoscalingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// NOTE: The reconciliation loop is being incrementally refactored so things may look a bit messy.
	// Changes in progress:
	// - reconcile loop will process one VA at a time. During the refactoring it does both, one and all

	// BEGIN: Per VA logic
	logger := ctrl.LoggerFrom(ctx)

	// Get the specific VA object that triggered this reconciliation
	var va llmdVariantAutoscalingV1alpha1.VariantAutoscaling
	if err := r.Get(ctx, req.NamespacedName, &va); err != nil { // Get returns, by default, a deep copy of the object
		if apierrors.IsNotFound(err) {
			logger.Info("VariantAutoscaling resource not found, may have been deleted",
				"name", req.Name,
				"namespace", req.Namespace)
			common.RemoveVACache(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Unable to fetch VariantAutoscaling",
			"name", req.Name,
			"namespace", req.Namespace)
		return ctrl.Result{}, err
	}

	// Keep a copy of the original object for Patch generation
	originalVA := va.DeepCopy()

	// Skip if the VA is being deleted
	if !va.DeletionTimestamp.IsZero() {
		logger.Info("VariantAutoscaling is being deleted, skipping reconciliation",
			"name", va.Name,
			"namespace", va.Namespace)
		common.RemoveVACache(req.NamespacedName)
		return ctrl.Result{}, nil
	}
	logger.Info("Reconciling VariantAutoscaling",
		"name", va.Name,
		"namespace", va.Namespace,
		"modelID", va.Spec.ModelID)

	// Attempts to resolve the target model variant using scaleTargetRef

	// Fetch scale target Deployment
	scaleTargetName := va.GetScaleTargetName()
	var deployment appsv1.Deployment
	if err := utils.GetDeploymentWithBackoff(ctx, r.Client, scaleTargetName, va.Namespace, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Scale target Deployment not found, waiting for deployment watch",
				"name", scaleTargetName,
				"namespace", va.Namespace)
			// Don't requeue - the deployment watch will trigger reconciliation
			// when the target deployment is created
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get scale target Deployment",
			"name", scaleTargetName,
			"namespace", va.Namespace)
		return ctrl.Result{}, err
	}

	logger.V(logging.DEBUG).Info(
		fmt.Sprintf("Scale target Deployment found: name=%s, namespace=%s", scaleTargetName, va.Namespace),
	)

	// Collect Metrics for this VA to populate Status.CurrentAlloc
	// We reuse CollectMetricsForSaturationMode which expects a map and list
	vaMap := make(map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling)
	if err := CollectMetricsForSaturationMode(ctx, []llmdVariantAutoscalingV1alpha1.VariantAutoscaling{va}, vaMap, r.Client, r.MetricsCollector); err != nil {
		logger.Error(err, "Failed to collect metrics", "variant", va.Name)
		// We continue to ensure decisions are applied even if metrics fail (though decision might depend on metrics)
	}
	// If the VA was updated during collection (it fetches fresh copy), update our local reference
	if updatedVA, ok := vaMap[va.Name]; ok {
		va = *updatedVA
	}

	// Process Engine Decisions from Shared Cache
	// This mechanism allows the Engine to trigger updates without touching the API server directly.
	if decision, ok := common.DecisionCache.Get(va.Name, va.Namespace); ok {
		// Only apply if the decision is fresher than the last one applied or if we haven't applied it
		// Note: We blindly apply for now, assuming the Engine acts as the source of truth for "Desired" state
		numReplicas, accelerator, lastRunTime := common.DecisionToOptimizedAlloc(decision)

		va.Status.DesiredOptimizedAlloc.NumReplicas = numReplicas
		va.Status.DesiredOptimizedAlloc.Accelerator = accelerator
		va.Status.DesiredOptimizedAlloc.LastRunTime = lastRunTime
	}

	// Update Status if we have changes (Conditions or OptimizedAlloc)
	// We use Patch to only send changed fields, avoiding validation errors on unchanged fields
	if err := r.Status().Patch(ctx, &va, client.MergeFrom(originalVA)); err != nil {
		logger.Error(err, "Failed to update VariantAutoscaling status",
			"name", va.Name)
		return ctrl.Result{}, err
	}

	// END: Per VA logic

	// Push updated VA to Engine cache
	common.UpdateVACache(&va)

	return ctrl.Result{}, nil
}

// BuildVariantStates extracts current and desired replica counts from VAs for capacity analysis.
func BuildVariantStates(
	ctx context.Context,
	vas []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	k8sClient client.Client,
) []interfaces.VariantReplicaState {

	states := make([]interfaces.VariantReplicaState, 0, len(vas))

	for _, va := range vas {
		// Get current replicas from deployment using ScaleTargetRef
		var deploy appsv1.Deployment
		if err := utils.GetDeploymentWithBackoff(ctx, k8sClient, va.GetScaleTargetName(), va.Namespace, &deploy); err != nil {
			ctrl.LoggerFrom(ctx).V(logging.DEBUG).Info("Could not get deployment for VA, skipping",
				"variant", va.Name,
				"error", err)
			continue
		}

		currentReplicas := int(deploy.Status.Replicas)
		if currentReplicas == 0 && deploy.Spec.Replicas != nil {
			currentReplicas = int(*deploy.Spec.Replicas)
		}

		states = append(states, interfaces.VariantReplicaState{
			VariantName:     deploy.Name,
			CurrentReplicas: currentReplicas,
			DesiredReplicas: va.Status.DesiredOptimizedAlloc.NumReplicas,
		})
	}

	return states
}

// RunSaturationAnalysis performs saturation analysis for a model and returns Saturation targets.
func RunSaturationAnalysis(
	ctx context.Context,
	modelID string,
	modelVAs []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	SaturationConfig interfaces.SaturationScalingConfig,
	k8sClient client.Client,
	metricsCollector interfaces.MetricsCollector,
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

	// Collect Saturation metrics using the configured collector
	replicaMetrics, err := metricsCollector.CollectReplicaMetrics(ctx, modelID, namespace, deployments, variantAutoscalings, variantCosts)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to collect Saturation metrics for model %s: %w", modelID, err)
	}

	logger.V(logging.DEBUG).Info("Collected Saturation metrics",
		"modelID", modelID,
		"namespace", namespace,
		"metricsCount", len(replicaMetrics))

	// If no metrics available, skip saturation analysis entirely
	// This prevents creating invalid decisions when pods are not ready or metrics are unavailable
	if len(replicaMetrics) == 0 {
		logger.V(logging.DEBUG).Info("No saturation metrics available for model, skipping analysis",
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

	logger.V(logging.DEBUG).Info("Saturation analysis completed",
		"modelID", modelID,
		"totalReplicas", saturationAnalysis.TotalReplicas,
		"nonSaturated", saturationAnalysis.NonSaturatedCount,
		"shouldScaleUp", saturationAnalysis.ShouldScaleUp,
		"scaleDownSafe", saturationAnalysis.ScaleDownSafe)

	// Build variant states (current and desired replicas)
	variantStates := BuildVariantStates(ctx, modelVAs, k8sClient)

	// Calculate saturation-based targets
	saturationTargets := saturationAnalyzer.CalculateSaturationTargets(ctx, saturationAnalysis, variantStates)

	logger.V(logging.DEBUG).Info("Saturation targets calculated",
		"modelID", modelID,
		"targets", saturationTargets)

	return saturationTargets, saturationAnalysis, variantStates, nil
}

// CollectMetricsForSaturationMode collects metrics and populates CurrentAlloc for VAs in saturation-only mode.
func CollectMetricsForSaturationMode(
	ctx context.Context,
	modelVAs []llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	vaMap map[string]*llmdVariantAutoscalingV1alpha1.VariantAutoscaling,
	k8sClient client.Client,
	metricsCollector interfaces.MetricsCollector,
) error {

	logger := ctrl.LoggerFrom(ctx)

	for i := range modelVAs {
		va := &modelVAs[i]
		modelName := va.Spec.ModelID

		// Get accelerator name from VA labels - required field
		accName := va.Labels["inference.optimization/acceleratorName"]
		if accName == "" {
			logger.V(logging.DEBUG).Info("Missing accelerator name label for VA, skipping",
				"variant", va.Name)
			continue
		}

		// Extract accelerator cost from VA.Spec.VariantCost - required field
		if va.Spec.VariantCost == "" {
			logger.V(logging.DEBUG).Info("Missing variant cost for VA, skipping",
				"variant", va.Name)
			continue
		}
		cost, err := strconv.ParseFloat(va.Spec.VariantCost, 64)
		if err != nil {
			logger.V(logging.DEBUG).Info("Invalid variant cost for VA, skipping",
				"variant", va.Name,
				"cost", va.Spec.VariantCost,
				"error", err)
			continue
		}

		// Get Deployment using ScaleTargetRef
		var deploy appsv1.Deployment
		err = utils.GetDeploymentWithBackoff(ctx, k8sClient, va.GetScaleTargetName(), va.Namespace, &deploy)
		if err != nil {
			logger.V(logging.DEBUG).Info("Could not get deployment for VA, skipping",
				"variant", va.Name,
				"deployment", va.GetScaleTargetName(),
				"error", err)
			continue // Skip VAs without deployments
		}

		// Fetch latest VA from API server (use VA name, not deployment name - they are now decoupled)
		var updateVA llmdVariantAutoscalingV1alpha1.VariantAutoscaling
		err = utils.GetVariantAutoscalingWithBackoff(ctx, k8sClient, va.Name, va.Namespace, &updateVA)
		if err != nil {
			logger.V(logging.DEBUG).Info("Unable to get VA",
				"variant", va.Name,
				"error", err)
			continue
		}

		// Validate metrics availability before collecting
		metricsValidation := metricsCollector.ValidateMetricsAvailability(ctx, modelName, deploy.Namespace)

		// Update MetricsAvailable condition based on validation result
		if metricsValidation.Available {
			llmdVariantAutoscalingV1alpha1.SetCondition(&updateVA,
				llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable,
				metav1.ConditionTrue,
				metricsValidation.Reason,
				metricsValidation.Message)
		} else {
			// Metrics unavailable - set condition and skip
			llmdVariantAutoscalingV1alpha1.SetCondition(&updateVA,
				llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable,
				metav1.ConditionFalse,
				metricsValidation.Reason,
				metricsValidation.Message)

			logger.V(logging.DEBUG).Info("Metrics unavailable for VA, skipping",
				"variant", updateVA.Name,
				"reason", metricsValidation.Reason,
				"troubleshooting", metricsValidation.Message)
			continue
		}

		// Collect raw metrics from collector
		metrics, err := metricsCollector.AddMetricsToOptStatus(ctx, &updateVA, deploy, cost)
		if err != nil {
			logger.V(logging.DEBUG).Info("Unable to fetch metrics for VA",
				"variant", updateVA.Name,
				"error", err)
			continue
		}

		// Assemble Allocation struct from raw metrics
		currentAllocation, err := BuildAllocationFromMetrics(metrics, &updateVA, deploy, cost)
		if err != nil {
			logger.V(logging.DEBUG).Info("Unable to build allocation for VA",
				"variant", updateVA.Name,
				"error", err)
			continue
		}

		// Update the VA in vaMap with populated CurrentAlloc
		updateVA.Status.CurrentAlloc = currentAllocation

		// Update vaMap with the VA that has CurrentAlloc populated
		vaMap[updateVA.Name] = &updateVA

		logger.V(logging.DEBUG).Info("Metrics collected for VA",
			"variant", updateVA.Name,
			"replicas", currentAllocation.NumReplicas,
			"accelerator", currentAllocation.Accelerator,
			"ttft", currentAllocation.TTFTAverage,
			"itl", currentAllocation.ITLAverage,
			"cost", currentAllocation.VariantCost)
	}

	return nil
}

// handleDeploymentEvent maps Deployment events to VA reconcile requests.
// When a Deployment is created, this finds any VAs that reference it and triggers reconciliation.
// This handles the race condition where VA is created before its target deployment.
func (r *VariantAutoscalingReconciler) handleDeploymentEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	deploy, ok := obj.(*appsv1.Deployment)
	if !ok {
		return nil
	}

	logger := ctrl.LoggerFrom(ctx)

	// List all VAs in the same namespace
	var vaList llmdVariantAutoscalingV1alpha1.VariantAutoscalingList
	if err := r.List(ctx, &vaList, client.InNamespace(deploy.Namespace)); err != nil {
		logger.Error(err, "Failed to list VAs for deployment event")
		return nil
	}

	// Find VAs that reference this deployment
	var requests []reconcile.Request
	for _, va := range vaList.Items {
		if va.GetScaleTargetName() == deploy.Name {
			logger.V(logging.DEBUG).Info("Deployment created, triggering VA reconciliation",
				"deployment", deploy.Name,
				"va", va.Name,
				"namespace", deploy.Namespace)
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: va.Namespace,
					Name:      va.Name,
				},
			})
		}
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *VariantAutoscalingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{}).
		// Watch the specific ConfigMap to trigger global reconcile and update shared config
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				// We expect a ConfigMap but check to be safe
				cm, ok := obj.(*corev1.ConfigMap)
				if !ok {
					return nil
				}

				logger := ctrl.LoggerFrom(ctx)
				name := cm.GetName()
				namespace := cm.GetNamespace()

				// Only interested in config maps in the configured namespace
				if namespace != configMapNamespace {
					return nil
				}

				if name == getConfigMapName() {
					// Optimization Config (Global Interval)
					if interval, ok := cm.Data["GLOBAL_OPT_INTERVAL"]; ok {
						common.Config.UpdateOptimizationConfig(interval)
						logger.Info("Updated global optimization config from ConfigMap", "interval", interval)
					}
					// Global config update is handled by the Engine loop which reads the new configuration.
					// No need to trigger immediate reconciliation for individual VAs.
					return nil
				} else if name == getSaturationConfigMapName() {
					// Saturation Scaling Config
					configs := make(map[string]interfaces.SaturationScalingConfig)
					count := 0
					for key, yamlStr := range cm.Data {
						var config interfaces.SaturationScalingConfig
						if err := yaml.Unmarshal([]byte(yamlStr), &config); err != nil {
							logger.Error(err, "Failed to parse saturation scaling config entry", "key", key)
							continue
						}
						// Validate
						if err := config.Validate(); err != nil {
							logger.Error(err, "Invalid saturation scaling config entry", "key", key)
							continue
						}
						configs[key] = config
						count++
					}
					common.Config.UpdateSaturationConfig(configs)
					logger.Info("Updated global saturation config from ConfigMap", "entries", count)

					// Global saturation config update is handled by the Engine loop.
					// No need to trigger immediate reconciliation for individual VAs.
					return nil
				}

				return nil
			}),
			// Predicate to filter only the target configmap
			builder.WithPredicates(ConfigMapPredicate()),
		).
		// Watch ServiceMonitor for controller's own metrics
		Watches(
			&promoperator.ServiceMonitor{},
			handler.EnqueueRequestsFromMapFunc(r.handleServiceMonitorEvent),
			builder.WithPredicates(ServiceMonitorPredicate()),
		).
		// Watch Deployments to trigger VA reconciliation when target deployment is created
		// This handles the race condition where VA is created before its target deployment
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.handleDeploymentEvent),
			builder.WithPredicates(DeploymentPredicate()),
		).
		// Watch DecisionTrigger channel for Engine decisions
		// This enables the Engine to trigger reconciliation without updating the object in API server
		WatchesRawSource(
			source.Channel(common.DecisionTrigger, &handler.EnqueueRequestForObject{}),
		).
		Named("variantAutoscaling").
		WithEventFilter(EventFilter()).
		Complete(r)
}

// handleServiceMonitorEvent handles events for the controller's own ServiceMonitor.
// When ServiceMonitor is deleted, it logs an error and emits a Kubernetes event.
// This ensures that administrators are aware when the ServiceMonitor that enables
// Prometheus scraping of controller metrics (including optimized replicas) is missing.
//
// Note: This handler does not enqueue reconcile requests. ServiceMonitor deletion doesn't
// affect the optimization logic (which reads from Prometheus), but it prevents future
// metrics from being scraped. The handler exists solely for observability - logging and
// emitting Kubernetes events to alert operators of the issue.
func (r *VariantAutoscalingReconciler) handleServiceMonitorEvent(ctx context.Context, obj client.Object) []reconcile.Request {
	serviceMonitor, ok := obj.(*promoperator.ServiceMonitor)
	if !ok {
		return nil
	}

	logger := ctrl.LoggerFrom(ctx)
	name := serviceMonitor.Name
	namespace := serviceMonitor.Namespace

	// Check if ServiceMonitor is being deleted
	if !serviceMonitor.GetDeletionTimestamp().IsZero() {
		logger.V(logging.VERBOSE).Info("ServiceMonitor being deleted - Prometheus will not scrape controller metrics",
			"servicemonitor", name,
			"namespace", namespace,
			"impact", "Actuator will not be able to access optimized replicas metrics",
			"action", "ServiceMonitor must be recreated for metrics scraping to resume")

		// Emit Kubernetes event for observability
		if r.Recorder != nil {
			r.Recorder.Eventf(
				serviceMonitor,
				corev1.EventTypeWarning,
				"ServiceMonitorDeleted",
				"ServiceMonitor %s/%s is being deleted. Prometheus will not scrape controller metrics. Actuator will not be able to access optimized replicas metrics. Please recreate the ServiceMonitor.",
				namespace,
				name,
			)
		}

		// Don't trigger reconciliation - ServiceMonitor deletion doesn't affect optimization logic
		return nil
	}

	// For create/update events, no action needed
	// Don't trigger reconciliation - ServiceMonitor changes don't affect optimization logic
	return nil
}
