// Package controller implements the Kubernetes controller for VariantAutoscaling resources.
//
// The controller package contains the core reconciliation logic for managing
// VariantAutoscaling custom resources. It orchestrates metrics collection,
// saturation analysis, optimization, and actuation to provide intelligent
// autoscaling for LLM inference workloads.
//
// # Architecture
//
// The VariantAutoscalingReconciler is the main controller that:
//   - Watches VariantAutoscaling, Deployment, and ServiceMonitor resources
//   - Collects metrics from Prometheus for inference servers
//   - Analyzes saturation levels and performance characteristics
//   - Calculates optimal replica counts
//   - Emits custom metrics for HPA/KEDA consumption
//   - Updates VariantAutoscaling status with current and desired state
//
// # Controller Instance Isolation
//
// The controller supports multi-instance isolation via the CONTROLLER_INSTANCE
// environment variable. When set, the controller:
//   - Only reconciles VAs with matching wva.llmd.ai/controller-instance label
//   - Adds controller_instance label to all emitted metrics
//   - Enables parallel testing and multi-tenant scenarios
//
// See docs/user-guide/multi-controller-isolation.md for details.
//
// # Reconciliation Flow
//
//  1. Fetch VariantAutoscaling resource
//  2. Validate scaleTargetRef (Deployment)
//  3. Collect metrics from Prometheus (request rate, latency, saturation)
//  4. Run saturation analysis engine
//  5. Calculate desired replicas based on saturation thresholds
//  6. Emit wva_desired_replicas metric to Prometheus
//  7. Update VariantAutoscaling status with allocations
//  8. Set conditions (Ready, MetricsAvailable, DeploymentFound, etc.)
//
// # Error Handling
//
// The controller uses controller-runtime conditions to report status:
//   - Ready: Overall health of autoscaling
//   - MetricsAvailable: Prometheus metrics accessibility
//   - DeploymentFound: Target deployment exists
//   - OptimizationSuccessful: Analysis completed without errors
//
// Transient errors trigger exponential backoff via controller-runtime.
//
// # Usage
//
// Controllers are registered in cmd/main.go:
//
//	if err := (&controller.VariantAutoscalingReconciler{
//		Client:   mgr.GetClient(),
//		Scheme:   mgr.GetScheme(),
//		Recorder: mgr.GetEventRecorderFor("variantautoscaling-controller"),
//		// ... other fields
//	}).SetupWithManager(mgr); err != nil {
//		setupLog.Error(err, "unable to create controller", "controller", "VariantAutoscaling")
//		os.Exit(1)
//	}
//
// See also:
//   - internal/engines/saturation: Saturation analysis engine
//   - internal/collector: Metrics collection
//   - internal/actuator: Metric emission
//   - docs/design/controller-behavior.md: Detailed behavior documentation
package controller
