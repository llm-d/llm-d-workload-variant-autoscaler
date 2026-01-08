// Package saturation implements saturation-based analysis for vLLM inference servers.
//
// The saturation package analyzes server metrics to determine optimal replica counts
// based on resource utilization, queue depth, and capacity planning.
//
// Core Concepts:
//
// Saturation Analysis measures how "full" a server is based on:
//   - KV Cache Utilization: Percentage of GPU memory used for KV cache
//   - Queue Depth: Number of requests waiting to be processed
//   - Slack Capacity: Available capacity before hitting saturation threshold
//
// The analyzer uses these metrics to determine:
//   - Current saturation level (0-100%)
//   - Slack capacity (requests that can be handled)
//   - Optimal replica count to maintain target utilization
//
// Example usage:
//
//	// Create saturation analyzer
//	analyzer := saturation.NewAnalyzer(config, logger)
//
//	// Analyze server metrics
//	result, err := analyzer.Analyze(ctx, metrics, currentReplicas)
//	if err != nil {
//	    log.Error(err, "saturation analysis failed")
//	    return err
//	}
//
//	log.Info("saturation analysis complete",
//	    "currentReplicas", result.CurrentReplicas,
//	    "desiredReplicas", result.DesiredReplicas,
//	    "saturation", result.SaturationLevel,
//	    "slackCapacity", result.SlackCapacity,
//	    "reason", result.ScalingReason)
//
// Scaling Logic:
//
// The analyzer applies the following logic:
//
//  1. If saturation > threshold: Scale up
//     - Calculate slack capacity deficit
//     - Add replicas to restore slack
//
//  2. If saturation < threshold: Consider scale down
//     - Check if replicas can be removed
//     - Apply cooldown period to prevent thrashing
//     - Respect min/max replica bounds
//
//  3. If queue depth > threshold: Scale up immediately
//     - Urgent scaling to prevent request queueing
//     - Bypasses normal saturation checks
//
// Configuration:
//
//	type SaturationConfig struct {
//	    TargetUtilization float64  // Target KV cache utilization (e.g., 0.8 = 80%)
//	    MinReplicas       int      // Minimum replica count
//	    MaxReplicas       int      // Maximum replica count
//	    ScaleUpStep       int      // Replicas to add per scale-up
//	    ScaleDownStep     int      // Replicas to remove per scale-down
//	    CooldownPeriod    Duration // Wait time between scaling actions
//	}
//
// The saturation package is designed to be:
//   - Predictable with clear scaling rules
//   - Configurable for different workload patterns
//   - Observable with detailed reasoning in logs
//   - Testable with comprehensive unit tests
package saturation
