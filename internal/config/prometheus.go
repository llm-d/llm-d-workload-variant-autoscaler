package config

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/utils"
)

// CacheConfig holds configuration for the metrics cache.
// This is the shared configuration type used by all collector plugins (Prometheus, EPP, etc.).
type CacheConfig struct {
	Enabled         bool
	TTL             time.Duration
	CleanupInterval time.Duration
	// FetchInterval is how often to fetch metrics in background (0 = disable background fetching)
	FetchInterval time.Duration
	// FreshnessThresholds define when metrics are considered fresh/stale/unavailable
	FreshnessThresholds FreshnessThresholds
}

// FreshnessThresholds defines when metrics are considered fresh, stale, or unavailable.
// This is the shared type used by all collector plugins.
type FreshnessThresholds struct {
	FreshThreshold       time.Duration // Metrics are fresh if age < this (default: 1 minute)
	StaleThreshold       time.Duration // Metrics are stale if age >= this but < unavailable (default: 2 minutes)
	UnavailableThreshold time.Duration // Metrics are unavailable if age >= this (default: 5 minutes)
}

// DetermineStatus determines the freshness status based on age.
// Returns "fresh", "stale", or "unavailable" based on the configured thresholds.
func (ft FreshnessThresholds) DetermineStatus(age time.Duration) string {
	if age < ft.FreshThreshold {
		return "fresh"
	} else if age < ft.UnavailableThreshold {
		return "stale"
	}
	return "unavailable"
}

// DefaultFreshnessThresholds returns default freshness thresholds
func DefaultFreshnessThresholds() FreshnessThresholds {
	return FreshnessThresholds{
		FreshThreshold:       1 * time.Minute,
		StaleThreshold:       2 * time.Minute,
		UnavailableThreshold: 5 * time.Minute,
	}
}

// PrometheusConfig retrieves Prometheus configuration from environment variables or ConfigMap
func PrometheusConfig(ctx context.Context, k8sClient client.Client) (*interfaces.PrometheusConfig, error) {
	// Try environment variables first
	config, err := PrometheusConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to get config from environment: %w", err)
	}
	if config != nil {
		return config, nil
	}

	// Try ConfigMap second
	config, err = PrometheusConfigFromConfigMap(ctx, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get config from ConfigMap: %w", err)
	}
	if config != nil {
		return config, nil
	}

	// No configuration found
	ctrl.Log.Info("No Prometheus configuration found. Please set PROMETHEUS_BASE_URL environment variable or configure via ConfigMap")
	return nil, fmt.Errorf("no Prometheus configuration found. Please set PROMETHEUS_BASE_URL environment variable or configure via ConfigMap")
}

// PrometheusConfigFromEnv retrieves Prometheus configuration from environment variables
func PrometheusConfigFromEnv() (*interfaces.PrometheusConfig, error) {
	promAddr := os.Getenv("PROMETHEUS_BASE_URL")
	if promAddr == "" {
		return nil, nil // No config found, but not an error
	}

	ctrl.Log.Info("Using Prometheus configuration from environment variables", "address", promAddr)
	return ParsePrometheusConfigFromEnv(), nil
}

// PrometheusConfigFromConfigMap retrieves Prometheus configuration from ConfigMap
func PrometheusConfigFromConfigMap(ctx context.Context, k8sClient client.Client) (*interfaces.PrometheusConfig, error) {
	cm := corev1.ConfigMap{}
	err := utils.GetConfigMapWithBackoff(ctx, k8sClient, ConfigMapName(), SystemNamespace(), &cm)
	if err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap for Prometheus config: %w", err)
	}

	promAddr, exists := cm.Data["PROMETHEUS_BASE_URL"]
	if !exists || promAddr == "" {
		return nil, nil // No config found, but not an error
	}

	ctrl.Log.Info("Using Prometheus configuration from ConfigMap", "address", promAddr)

	config := &interfaces.PrometheusConfig{
		BaseURL: promAddr,
	}

	// Parse TLS configuration from ConfigMap (TLS is always enabled for HTTPS-only support)
	config.InsecureSkipVerify = ConfigValue(cm.Data, "PROMETHEUS_TLS_INSECURE_SKIP_VERIFY", "") == "true"
	config.CACertPath = ConfigValue(cm.Data, "PROMETHEUS_CA_CERT_PATH", "")
	config.ClientCertPath = ConfigValue(cm.Data, "PROMETHEUS_CLIENT_CERT_PATH", "")
	config.ClientKeyPath = ConfigValue(cm.Data, "PROMETHEUS_CLIENT_KEY_PATH", "")
	config.ServerName = ConfigValue(cm.Data, "PROMETHEUS_SERVER_NAME", "")

	// Add bearer token if provided
	if bearerToken, exists := cm.Data["PROMETHEUS_BEARER_TOKEN"]; exists && bearerToken != "" {
		config.BearerToken = bearerToken
	}

	return config, nil
}

// ParsePrometheusCacheConfigFromData parses Prometheus collector cache configuration from ConfigMap data.
// This is used for runtime updates when the ConfigMap changes.
// Returns nil if no cache config keys are present in the data.
func ParsePrometheusCacheConfigFromData(data map[string]string) *CacheConfig {
	// Check if any cache config keys are present
	cacheKeys := []string{
		"PROMETHEUS_METRICS_CACHE_ENABLED",
		"PROMETHEUS_METRICS_CACHE_TTL",
		"PROMETHEUS_METRICS_CACHE_CLEANUP_INTERVAL",
		"PROMETHEUS_METRICS_CACHE_FETCH_INTERVAL",
		"PROMETHEUS_METRICS_CACHE_FRESH_THRESHOLD",
		"PROMETHEUS_METRICS_CACHE_STALE_THRESHOLD",
		"PROMETHEUS_METRICS_CACHE_UNAVAILABLE_THRESHOLD",
	}
	hasCacheConfig := false
	for _, key := range cacheKeys {
		if _, ok := data[key]; ok {
			hasCacheConfig = true
			break
		}
	}
	if !hasCacheConfig {
		return nil // No cache config keys present
	}

	// Initialize with defaults including freshness thresholds
	defaultThresholds := DefaultFreshnessThresholds()
	config := &CacheConfig{
		Enabled:             true, // default
		TTL:                 30 * time.Second,
		CleanupInterval:     1 * time.Minute,
		FetchInterval:       30 * time.Second, // default fetch interval
		FreshnessThresholds: defaultThresholds,
	}

	// PROMETHEUS_METRICS_CACHE_ENABLED (default: true)
	config.Enabled = ParseBoolFromConfig(data, "PROMETHEUS_METRICS_CACHE_ENABLED", true)

	// PROMETHEUS_METRICS_CACHE_TTL (default: 30s)
	config.TTL = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_TTL", 30*time.Second)

	// PROMETHEUS_METRICS_CACHE_CLEANUP_INTERVAL (default: 1m)
	config.CleanupInterval = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_CLEANUP_INTERVAL", 1*time.Minute)

	// PROMETHEUS_METRICS_CACHE_FETCH_INTERVAL (default: 30s, 0 = disable background fetching)
	config.FetchInterval = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_FETCH_INTERVAL", 30*time.Second)

	// Freshness thresholds
	// PROMETHEUS_METRICS_CACHE_FRESH_THRESHOLD (default: 1m)
	config.FreshnessThresholds.FreshThreshold = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_FRESH_THRESHOLD", 1*time.Minute)

	// PROMETHEUS_METRICS_CACHE_STALE_THRESHOLD (default: 2m)
	config.FreshnessThresholds.StaleThreshold = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_STALE_THRESHOLD", 2*time.Minute)

	// PROMETHEUS_METRICS_CACHE_UNAVAILABLE_THRESHOLD (default: 5m)
	config.FreshnessThresholds.UnavailableThreshold = ParseDurationFromConfig(data, "PROMETHEUS_METRICS_CACHE_UNAVAILABLE_THRESHOLD", 5*time.Minute)

	return config
}

// ReadPrometheusCacheConfig reads Prometheus collector cache configuration from the ConfigMap
func ReadPrometheusCacheConfig(ctx context.Context, k8sClient client.Client) (*CacheConfig, error) {
	cm := corev1.ConfigMap{}
	err := utils.GetConfigMapWithBackoff(ctx, k8sClient, ConfigMapName(), SystemNamespace(), &cm)
	if err != nil {
		return nil, fmt.Errorf("failed to get configmap for Prometheus cache config: %w", err)
	}

	return ParsePrometheusCacheConfigFromData(cm.Data), nil
}

// ParsePrometheusConfigFromEnv parses Prometheus configuration from environment variables.
// Supports both direct values and file paths for flexible deployment scenarios.
func ParsePrometheusConfigFromEnv() *interfaces.PrometheusConfig {
	config := &interfaces.PrometheusConfig{
		BaseURL: os.Getenv("PROMETHEUS_BASE_URL"),
	}

	// TLS is always enabled for HTTPS-only support
	config.InsecureSkipVerify = os.Getenv("PROMETHEUS_TLS_INSECURE_SKIP_VERIFY") == "true"
	config.CACertPath = os.Getenv("PROMETHEUS_CA_CERT_PATH")
	config.ClientCertPath = os.Getenv("PROMETHEUS_CLIENT_CERT_PATH")
	config.ClientKeyPath = os.Getenv("PROMETHEUS_CLIENT_KEY_PATH")
	config.ServerName = os.Getenv("PROMETHEUS_SERVER_NAME")

	// Support both direct bearer token and token path
	config.BearerToken = os.Getenv("PROMETHEUS_BEARER_TOKEN")
	config.TokenPath = os.Getenv("PROMETHEUS_TOKEN_PATH")

	return config
}
