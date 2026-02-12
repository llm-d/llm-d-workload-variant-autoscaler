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
	"time"

	"github.com/go-logr/logr"
	yaml "gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/constants"
	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
)

// handleMutableParameters processes mutable configuration parameters from ConfigMap data.
// Filters out immutable keys and updates the Config object accordingly.
func handleMutableParameters(ctx context.Context, cfg *config.Config, cmData map[string]string, immutableKeys map[string]bool, logger logr.Logger) {
	// Optimization Config (Global Interval) - mutable parameter
	if interval, ok := cmData["GLOBAL_OPT_INTERVAL"]; ok && !immutableKeys["GLOBAL_OPT_INTERVAL"] {
		if parsedInterval, err := time.ParseDuration(interval); err == nil {
			cfg.UpdateOptimizationInterval(parsedInterval)
			logger.Info("Updated global optimization config from ConfigMap", "interval", interval)
		} else {
			logger.Error(err, "Invalid GLOBAL_OPT_INTERVAL in ConfigMap", "value", interval)
		}
	}

	// Prometheus cache configuration - mutable parameter
	// Check if any cache config keys are present and not in immutable keys
	cacheConfigKeys := []string{
		"PROMETHEUS_METRICS_CACHE_ENABLED",
		"PROMETHEUS_METRICS_CACHE_TTL",
		"PROMETHEUS_METRICS_CACHE_CLEANUP_INTERVAL",
		"PROMETHEUS_METRICS_CACHE_FETCH_INTERVAL",
		"PROMETHEUS_METRICS_CACHE_FRESH_THRESHOLD",
		"PROMETHEUS_METRICS_CACHE_STALE_THRESHOLD",
		"PROMETHEUS_METRICS_CACHE_UNAVAILABLE_THRESHOLD",
	}
	hasCacheConfig := false
	for _, key := range cacheConfigKeys {
		if _, ok := cmData[key]; ok && !immutableKeys[key] {
			hasCacheConfig = true
			break
		}
	}
	if hasCacheConfig {
		// Parse and update Prometheus cache config
		cacheConfig := config.ParsePrometheusCacheConfigFromData(cmData)
		if cacheConfig != nil {
			cfg.UpdatePrometheusCacheConfig(cacheConfig)
			logger.Info("Updated Prometheus cache config from ConfigMap",
				"enabled", cacheConfig.Enabled,
				"ttl", cacheConfig.TTL,
				"fetchInterval", cacheConfig.FetchInterval)
		}
	}
}

// parseSaturationConfig parses saturation scaling configuration from ConfigMap data.
// Returns the parsed configs and count of successfully parsed entries.
func parseSaturationConfig(cmData map[string]string, logger logr.Logger) (config.SaturationScalingConfigPerModel, int) {
	configs := make(config.SaturationScalingConfigPerModel)
	count := 0
	for key, yamlStr := range cmData {
		var satConfig interfaces.SaturationScalingConfig
		if err := yaml.Unmarshal([]byte(yamlStr), &satConfig); err != nil {
			logger.Error(err, "Failed to parse saturation scaling config entry", "key", key)
			continue
		}
		// Validate
		if err := satConfig.Validate(); err != nil {
			logger.Error(err, "Invalid saturation scaling config entry", "key", key)
			continue
		}
		configs[key] = satConfig
		count++
	}
	return configs, count
}

// isNamespaceConfigEnabled checks if a namespace has the opt-in label for namespace-local ConfigMaps.
// This allows namespaces to opt-in for ConfigMap watching even before VAs are created.
// Package-level function so it can be used by both reconcilers.
func isNamespaceConfigEnabled(ctx context.Context, c client.Client, namespace string) bool {
	if namespace == "" {
		return false
	}

	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		// If namespace doesn't exist or we can't read it, default to not enabled
		// This is safe - we'll proceed with normal logic
		return false
	}

	labels := ns.GetLabels()
	if labels == nil {
		return false
	}

	value, exists := labels[constants.NamespaceConfigEnabledLabelKey]
	return exists && value == "true"
}

// isNamespaceExcluded checks if a namespace has the exclude annotation.
// Excluded namespaces are not watched for ConfigMaps or reconciled for VAs.
// Thread-safe (reads namespace object from API server).
// Package-level function so it can be used by both reconcilers.
func isNamespaceExcluded(ctx context.Context, c client.Client, namespace string) bool {
	if namespace == "" {
		return false
	}

	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		// If namespace doesn't exist or we can't read it, default to not excluded
		// This is safe - we'll proceed with normal logic
		return false
	}

	annotations := ns.GetAnnotations()
	if annotations == nil {
		return false
	}

	value, exists := annotations[constants.NamespaceExcludeAnnotationKey]
	return exists && value == "true"
}
