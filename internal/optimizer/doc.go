// Package optimizer implements the optimization engine for WVA.
//
// The optimizer package orchestrates the autoscaling decision-making process by
// coordinating metrics collection, saturation analysis, and solver invocation.
//
// Architecture:
//
// The optimizer follows a pipeline pattern:
//
//	Metrics Collection → Saturation Analysis → Solver → Actuator
//	      (Collector)        (Saturation)      (Solver)  (Actuator)
//
// The optimizer sits in the middle, orchestrating these components.
//
// Example usage:
//
//	// Create optimizer with dependencies
//	opt := optimizer.NewOptimizer(
//	    collector,
//	    saturationAnalyzer,
//	    solver,
//	    actuator,
//	    config,
//	    logger,
//	)
//
//	// Run optimization for a VariantAutoscaling resource
//	result, err := opt.Optimize(ctx, variantAutoscaling)
//	if err != nil {
//	    log.Error(err, "optimization failed")
//	    return err
//	}
//
//	log.Info("optimization complete",
//	    "variant", variantAutoscaling.Name,
//	    "currentReplicas", result.CurrentReplicas,
//	    "desiredReplicas", result.DesiredReplicas,
//	    "reason", result.Reason)
//
// Optimization Flow:
//
//  1. Collect Metrics
//     - Fetch vLLM metrics from Prometheus
//     - Validate metrics are fresh and complete
//     - Update MetricsAvailable condition
//
//  2. Analyze Saturation
//     - Calculate current saturation level
//     - Determine slack capacity
//     - Identify scaling trigger (if any)
//
//  3. Invoke Solver
//     - Run optimization algorithm
//     - Calculate desired replica count
//     - Apply scaling policies and bounds
//
//  4. Actuate
//     - Emit metrics to Prometheus
//     - Update VariantAutoscaling status
//     - Log scaling decision
//
// Error Handling:
//
// The optimizer handles errors at each stage:
//   - Metrics collection failures → Set MetricsAvailable=False
//   - Analysis failures → Log and skip optimization
//   - Solver failures → Set OptimizationReady=False
//   - Actuation failures → Retry with backoff
//
// The optimizer is designed to be:
//   - Composable with dependency injection
//   - Observable with structured logging and metrics
//   - Testable with mock dependencies
//   - Resilient to partial failures
package optimizer
