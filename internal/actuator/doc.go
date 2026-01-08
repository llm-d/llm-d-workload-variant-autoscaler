// Package actuator implements the actuator pattern for WVA scaling decisions.
//
// The actuator package is responsible for exposing optimization results as Prometheus
// metrics and updating VariantAutoscaling status in Kubernetes. It acts as the
// "write side" of the autoscaling loop.
//
// Responsibilities:
//
//  1. Emit custom metrics to Prometheus
//     - inferno_current_replicas
//     - inferno_desired_replicas
//     - inferno_desired_ratio
//     - inferno_replica_scaling_total
//
//  2. Update VariantAutoscaling status
//     - Current and desired allocations
//     - Status conditions (MetricsAvailable, OptimizationReady)
//     - Timestamps and reasons
//
//  3. Integrate with HPA/KEDA
//     - Expose metrics in format expected by autoscalers
//     - Maintain metric labels for proper matching
//
// Example usage:
//
//	// Create actuator
//	actuator := actuator.NewActuator(
//	    k8sClient,
//	    metricsRegistry,
//	    logger,
//	)
//
//	// Update status and emit metrics
//	err := actuator.Actuate(ctx, &actuator.ActuatorInput{
//	    VariantAutoscaling: va,
//	    CurrentReplicas:    3,
//	    DesiredReplicas:    5,
//	    SaturationLevel:    0.85,
//	    ScalingReason:      "saturation threshold exceeded",
//	    OptimizationResult: result,
//	})
//	if err != nil {
//	    log.Error(err, "actuation failed")
//	    return err
//	}
//
// Metrics Emission:
//
// The actuator emits metrics with consistent labels:
//
//	inferno_desired_ratio{
//	    variant_name="llama-8b-autoscaler",
//	    namespace="llm-inference",
//	    accelerator_type="NVIDIA-H100-80GB"
//	} = 1.67  # desired/current = 5/3
//
// HPA/KEDA reads this metric to perform actual scaling.
//
// Status Updates:
//
// The actuator updates VariantAutoscaling status with:
//
//	status:
//	  currentReplicas: 3
//	  desiredReplicas: 5
//	  conditions:
//	  - type: MetricsAvailable
//	    status: "True"
//	    reason: MetricsFound
//	    lastTransitionTime: "2026-01-08T15:00:00Z"
//	  - type: OptimizationReady
//	    status: "True"
//	    reason: OptimizationSucceeded
//	    lastTransitionTime: "2026-01-08T15:00:00Z"
//
// Error Handling:
//
// The actuator handles partial failures gracefully:
//   - Metric emission failures are logged but don't block status updates
//   - Status update failures are retried with exponential backoff
//   - Condition transitions are tracked for observability
//
// The actuator package is designed to be:
//   - Reliable with retry logic and error handling
//   - Observable with detailed logging and metrics
//   - Decoupled from optimization logic (receives results)
//   - Testable with mock clients and metrics registries
package actuator
