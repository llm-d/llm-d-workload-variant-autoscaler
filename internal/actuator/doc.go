// Package actuator handles metric emission and deployment actuations.
//
// The actuator package is responsible for emitting metrics to Prometheus
// and (optionally) applying scaling decisions to Kubernetes deployments.
// In WVA's architecture, the actuator exposes custom metrics that HPA/KEDA
// consume for actual scaling.
//
// # Architecture
//
// WVA uses an indirect actuation model:
//
//	WVA Controller → Actuator → Prometheus Metrics → HPA/KEDA → Deployment
//
// This design provides:
//   - Separation of concerns (WVA calculates, HPA executes)
//   - Standardized metric interface
//   - Compatibility with existing autoscaling infrastructure
//   - Gradual rollout via HPA stabilization windows
//
// # Actuator Responsibilities
//
//  1. Metric Emission:
//     - Emit wva_desired_replicas gauge to Prometheus
//     - Add controller_instance label for multi-controller isolation
//     - Include namespace, deployment, model_id labels
//
//  2. Status Queries:
//     - Query current deployment replica count
//     - Fetch deployment status and readiness
//
//  3. (Future) Direct Actuation:
//     - Update Deployment.spec.replicas directly
//     - Handle scale-to-zero scenarios
//     - Implement gradual rollout strategies
//
// # Metric Emission
//
// The actuator emits a single primary metric:
//
//	wva_desired_replicas{
//	  namespace="llm-inference",
//	  deployment="llama-8b",
//	  model_id="meta/llama-3.1-8b",
//	  controller_instance="test-1"
//	} = 5
//
// HPA configuration to consume this metric:
//
//	apiVersion: autoscaling/v2
//	kind: HorizontalPodAutoscaler
//	spec:
//	  metrics:
//	  - type: Object
//	    object:
//	      metric:
//	        name: wva_desired_replicas
//	        selector:
//	          matchLabels:
//	            controller_instance: "test-1"  # Optional
//	      describedObject:
//	        apiVersion: llmd.ai/v1alpha1
//	        kind: VariantAutoscaling
//	        name: llama-8b-autoscaler
//	      target:
//	        type: Value
//	        value: "1"
//
// # MetricsEmitter
//
// The core component for metric emission:
//
//	type MetricsEmitter struct {
//	    // Prometheus registry
//	}
//
//	emitter := metrics.NewMetricsEmitter()
//	emitter.EmitDesiredReplicas(namespace, deployment, modelID, replicas)
//
// Metrics are exposed on the controller's /metrics endpoint (default :8443).
//
// # Controller Instance Labels
//
// When CONTROLLER_INSTANCE environment variable is set:
//
//	emitter.EmitDesiredReplicas(ns, deploy, modelID, replicas)
//	// Emits: wva_desired_replicas{controller_instance="$CONTROLLER_INSTANCE", ...}
//
// This enables:
//   - Parallel E2E testing (each test uses unique controller instance)
//   - Multi-tenant deployments (isolate metrics per tenant)
//   - Canary deployments (test new controller alongside production)
//
// See docs/user-guide/multi-controller-isolation.md for use cases.
//
// # Actuation Status
//
// The actuator updates VariantAutoscaling.status.actuation:
//
//	type ActuationStatus struct {
//	    Applied bool  // Whether metrics were successfully emitted
//	}
//
// This field tracks whether the desired replica count was successfully
// communicated to the autoscaling system.
//
// # Error Handling
//
// Common errors:
//   - ErrDeploymentNotFound: Target deployment doesn't exist
//   - ErrMetricEmissionFailed: Prometheus push gateway unavailable
//   - ErrInvalidReplicaCount: Negative or exceeds max replicas
//
// Errors are logged and reflected in VariantAutoscaling conditions.
//
// # Usage Example
//
//	import "github.com/llm-d-incubation/workload-variant-autoscaler/internal/actuator"
//
//	// Create actuator
//	act := actuator.NewActuator(k8sClient)
//
//	// Get current replicas
//	currentReplicas, err := act.GetCurrentDeploymentReplicas(ctx, va)
//
//	// Emit desired replicas
//	err = act.MetricsEmitter.EmitDesiredReplicas(
//	    va.Namespace,
//	    va.GetScaleTargetName(),
//	    va.Spec.ModelID,
//	    desiredReplicas,
//	)
//
// # Future Enhancements
//
// Planned features:
//   - Direct deployment scaling (bypass HPA for faster response)
//   - Gradual rollout strategies (incremental scale-up/down)
//   - Scale-to-zero with warm-up handling
//   - Multi-metric actuation (CPU, memory, custom metrics)
//
// See also:
//   - internal/metrics: Prometheus metric definitions
//   - docs/integrations/hpa-integration.md: HPA setup guide
//   - docs/integrations/keda-integration.md: KEDA integration
//   - docs/user-guide/multi-controller-isolation.md: Multi-controller setup
package actuator
