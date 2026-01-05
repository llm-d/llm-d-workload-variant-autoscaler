package controller

import (
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
		return (name == getConfigMapName() || name == getSaturationConfigMapName()) && obj.GetNamespace() == configMapNamespace
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
// It allows Create events for all Deployments (to trigger VA reconciliation when target is created).
// This handles the race condition where VA is created before its target deployment.
func DeploymentPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			// Controller-runtime guarantees e.Object is a Deployment since we watch that type
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}
