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

	promoperator "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/utils"
)

// VariantAutoscalingReconciler reconciles a variantAutoscaling object
type VariantAutoscalingReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Recorder record.EventRecorder
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

			// Update status to reflect target not found
			llmdVariantAutoscalingV1alpha1.SetCondition(&va,
				llmdVariantAutoscalingV1alpha1.TypeTargetResolved,
				metav1.ConditionFalse,
				llmdVariantAutoscalingV1alpha1.ReasonTargetNotFound,
				fmt.Sprintf("Scale target Deployment %s not found", scaleTargetName))

			if err := r.Status().Patch(ctx, &va, client.MergeFrom(originalVA)); err != nil {
				logger.Error(err, "Failed to update VariantAutoscaling status")
				return ctrl.Result{}, err
			}

			// Don't requeue - the deployment watch will trigger reconciliation
			// when the target deployment is created
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get scale target Deployment",
			"name", scaleTargetName,
			"namespace", va.Namespace)
		return ctrl.Result{}, err
	}

	// Target found
	llmdVariantAutoscalingV1alpha1.SetCondition(&va,
		llmdVariantAutoscalingV1alpha1.TypeTargetResolved,
		metav1.ConditionTrue,
		llmdVariantAutoscalingV1alpha1.ReasonTargetFound,
		fmt.Sprintf("Scale target Deployment %s found", scaleTargetName))

	logger.V(logging.DEBUG).Info(
		fmt.Sprintf("Scale target Deployment found: name=%s, namespace=%s", scaleTargetName, va.Namespace),
	)

	// Process Engine Decisions from Shared Cache
	// This mechanism allows the Engine to trigger updates without touching the API server directly.
	if decision, ok := common.DecisionCache.Get(va.Name, va.Namespace); ok {
		logger.Info("Found decision in cache", "va", va.Name, "namespace", va.Namespace, "metricsAvailable", decision.MetricsAvailable)
		// Only apply if the decision is fresher than the last one applied or if we haven't applied it
		// Note: We blindly apply for now, assuming the Engine acts as the source of truth for "Desired" state
		numReplicas, accelerator, lastRunTime := common.DecisionToOptimizedAlloc(decision)

		// Only update DesiredOptimizedAlloc if we have valid values (accelerator is required by CRD)
		if accelerator != "" && numReplicas > 0 {
			va.Status.DesiredOptimizedAlloc.NumReplicas = numReplicas
			va.Status.DesiredOptimizedAlloc.Accelerator = accelerator
			va.Status.DesiredOptimizedAlloc.LastRunTime = lastRunTime
		}

		// Always apply MetricsAvailable condition from cache
		metricsStatus := metav1.ConditionFalse
		if decision.MetricsAvailable {
			metricsStatus = metav1.ConditionTrue
		}
		llmdVariantAutoscalingV1alpha1.SetCondition(&va,
			llmdVariantAutoscalingV1alpha1.TypeMetricsAvailable,
			metricsStatus,
			decision.MetricsReason,
			decision.MetricsMessage)

		// Note: CurrentAlloc is removed from Status.
		// Internal allocation state is managed by the Engine and Actuator.
	} else {
		logger.Info("No decision found in cache for VA", "va", va.Name, "namespace", va.Namespace)
	}

	// Update Status if we have changes (Conditions or OptimizedAlloc)
	// We use Patch to only send changed fields, avoiding validation errors on unchanged fields
	if err := r.Status().Patch(ctx, &va, client.MergeFrom(originalVA)); err != nil {
		logger.Error(err, "Failed to update VariantAutoscaling status",
			"name", va.Name)
		return ctrl.Result{}, err
	}

	// END: Per VA logic

	return ctrl.Result{}, nil
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
		For(&llmdVariantAutoscalingV1alpha1.VariantAutoscaling{},
			// Filter VAs by controller-instance label for multi-controller isolation
			builder.WithPredicates(VariantAutoscalingPredicate()),
		).
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
