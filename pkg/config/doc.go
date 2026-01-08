// Package config provides configuration management for the WVA controller.
//
// This package handles loading, validation, and access to controller configuration
// from various sources including ConfigMaps, environment variables, and command-line flags.
//
// Configuration Types:
//
//   - ControllerConfig: Main controller settings (reconcile interval, workers, etc.)
//   - SaturationConfig: Saturation-based scaling parameters
//   - PrometheusConfig: Prometheus connection and query settings
//   - AcceleratorCosts: GPU cost definitions for optimization
//   - ServiceClasses: SLO definitions and QoS requirements
//
// Configuration Sources:
//
//  1. Command-line flags (highest priority)
//  2. Environment variables
//  3. ConfigMaps (Kubernetes)
//  4. Default values (lowest priority)
//
// Example usage:
//
//	// Load configuration from default sources
//	cfg, err := config.Load()
//	if err != nil {
//	    log.Fatal(err, "failed to load configuration")
//	}
//
//	// Access configuration values
//	log.Info("controller configuration",
//	    "reconcileInterval", cfg.ReconcileInterval,
//	    "prometheusURL", cfg.Prometheus.URL,
//	    "saturationThreshold", cfg.Saturation.TargetUtilization)
//
//	// Watch for configuration changes (ConfigMap updates)
//	watcher, err := config.Watch(ctx, cfg)
//	if err != nil {
//	    log.Error(err, "failed to start config watcher")
//	}
//	defer watcher.Stop()
//
//	go func() {
//	    for newCfg := range watcher.Updates() {
//	        log.Info("configuration updated", "config", newCfg)
//	        // Apply new configuration
//	    }
//	}()
//
// Configuration Validation:
//
// All configuration values are validated on load:
//   - Numeric ranges (e.g., 0.0 < target_utilization < 1.0)
//   - Required fields (e.g., Prometheus URL)
//   - Format validation (e.g., duration strings)
//   - Cross-field constraints (e.g., min < max replicas)
//
// The config package is designed to be:
//   - Type-safe with strong typing
//   - Validated at load time
//   - Observable with structured logging
//   - Hot-reloadable for ConfigMap changes
package config
