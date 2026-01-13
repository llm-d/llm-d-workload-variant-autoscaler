package controller

import (
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/constants"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/metrics"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigMapPredicate returns a predicate that filters ConfigMap events to only the target ConfigMaps.
// It matches the enqueue function logic - allows either configmap name if namespace matches.
// This predicate is used to filter only the target configmaps.
func ConfigMapPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		name := obj.GetName()
		return (name == getConfigMapName() || name == getModelScalingConfigMapName()) && obj.GetNamespace() == configMapNamespace
	})
}

// ServiceMonitorPredicate returns a predicate that filters ServiceMonitor events to only the target ServiceMonitor.
// It checks that the ServiceMonitor name matches serviceMonitorName and namespace matches configMapNamespace.
// This predicate is used to filter only the target ServiceMonitor.
// The ServiceMonitor is watched to enable detection when it is deleted, which would prevent
// Prometheus from scraping controller metrics (including optimized replicas).
func ServiceMonitorPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == defaultServiceMonitorName && obj.GetNamespace() == configMapNamespace
	})
}

// EventFilter returns a predicate.Funcs that filters events for the VariantAutoscaling controller.
// It allows:
//   - All Create events
//   - Update events for ConfigMap (needed to trigger reconcile on config changes)
//   - Update events for ServiceMonitor when deletionTimestamp is set (finalizers cause deletion to emit Update events)
//   - Delete events for ServiceMonitor (for immediate deletion detection)
//
// It blocks:
//   - Update events for VariantAutoscaling resource (controller reconciles periodically, so individual updates are unnecessary)
//   - Delete events for VariantAutoscaling resource (controller reconciles periodically and filters out deleted resources)
//   - Generic events
func EventFilter() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			gvk := e.ObjectNew.GetObjectKind().GroupVersionKind()
			// Allow Update events for ConfigMap (needed to trigger reconcile on config changes)
			if gvk.Kind == "ConfigMap" && gvk.Group == "" {
				return true
			}
			// Allow Update events for ServiceMonitor when deletionTimestamp is set
			// (finalizers cause deletion to emit Update events with deletionTimestamp)
			if gvk.Group == serviceMonitorGVK.Group && gvk.Kind == serviceMonitorGVK.Kind {
				// Check if deletionTimestamp was just set (deletion started)
				if deletionTimestamp := e.ObjectNew.GetDeletionTimestamp(); deletionTimestamp != nil && !deletionTimestamp.IsZero() {
					// Check if this is a newly set deletion timestamp
					oldDeletionTimestamp := e.ObjectOld.GetDeletionTimestamp()
					if oldDeletionTimestamp == nil || oldDeletionTimestamp.IsZero() {
						return true // Deletion just started
					}
				}
			}
			// Block Update events for VariantAutoscaling resource.
			// The controller reconciles all VariantAutoscaling resources periodically (every 60s by default),
			// so individual resource update events would only cause unnecessary reconciles without benefit.
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			gvk := e.Object.GetObjectKind().GroupVersionKind()
			// Allow Delete events for ServiceMonitor (for immediate deletion detection)
			if gvk.Group == serviceMonitorGVK.Group && gvk.Kind == serviceMonitorGVK.Kind {
				return true
			}
			// Block Delete events for VariantAutoscaling resource.
			// The controller reconciles all VariantAutoscaling resources periodically and filters out
			// deleted resources in filterActiveVariantAutoscalings, so individual delete events
			// would only cause unnecessary reconciles without benefit.
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// DeploymentPredicate returns a predicate that filters Deployment events.
// It allows Create and Delete events for all Deployments to trigger VA reconciliation:
// - Create: handles the race condition where VA is created before its target deployment
// - Delete: allows VA to update status and clear metrics when target deployment is removed
func DeploymentPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Allow all Deployment create events to trigger reconciliation
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Allow all Deployment delete events to trigger reconciliation
			// so VAs can update their status when target deployment is removed
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// VariantAutoscalingPredicate returns a predicate that filters VariantAutoscaling events
// based on the controller instance label. This enables multi-controller isolation where
// each controller instance only manages VAs that are explicitly assigned to it.
//
// Filtering behavior:
//   - If CONTROLLER_INSTANCE env var is not set: allow all VAs (backwards compatible)
//   - If CONTROLLER_INSTANCE is set: only allow VAs with matching wva.llmd.ai/controller-instance label
//
// This predicate should be used with the VA watch to ensure controllers only reconcile
// their assigned VAs, preventing conflicts when multiple controllers run simultaneously.
func VariantAutoscalingPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		controllerInstance := metrics.GetControllerInstance()

		// If no controller instance configured, allow all VAs (backwards compatible)
		if controllerInstance == "" {
			return true
		}

		// Only allow VAs with matching controller-instance label
		labels := obj.GetLabels()
		if labels == nil {
			return false
		}

		vaInstance, hasLabel := labels[constants.ControllerInstanceLabelKey]
		return hasLabel && vaInstance == controllerInstance
	})
}
