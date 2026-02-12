package config

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
)

// Config is the unified configuration structure for the WVA controller.
// All fields are private and accessed via thread-safe getter methods.
type Config struct {
	mu sync.RWMutex // Single mutex for all mutable fields

	infrastructure infrastructureConfig
	tls            tlsConfig
	prometheus     prometheusConfig
	epp            eppConfig
	optimization   optimizationConfig
	features       featureFlagsConfig
	saturation     saturationConfig  // namespace-aware
	scaleToZero    scaleToZeroConfig // namespace-aware
}

// infrastructureConfig holds server/controller infrastructure settings
type infrastructureConfig struct {
	metricsAddr          string
	probeAddr            string
	enableLeaderElection bool
	leaderElectionID     string
	leaseDuration        time.Duration
	renewDeadline        time.Duration
	retryPeriod          time.Duration
	restTimeout          time.Duration
	secureMetrics        bool
	enableHTTP2          bool
	watchNamespace       string
	loggerVerbosity      int
}

// tlsConfig holds TLS certificate paths
type tlsConfig struct {
	webhookCertPath string
	webhookCertName string
	webhookCertKey  string
	metricsCertPath string
	metricsCertName string
	metricsCertKey  string
}

// prometheusConfig holds all Prometheus-related configuration
// (both connection settings and cache config)
type prometheusConfig struct {
	// Immutable (set at startup)
	baseURL            string
	bearerToken        string
	tokenPath          string
	insecureSkipVerify bool
	caCertPath         string
	clientCertPath     string
	clientKeyPath      string
	serverName         string

	// Mutable (can change at runtime)
	cache *CacheConfig
}

// eppConfig holds EPP (Endpoint Pool) integration configuration
type eppConfig struct {
	metricReaderBearerToken string
}

// optimizationConfig holds optimization settings
type optimizationConfig struct {
	interval time.Duration
}

// featureFlagsConfig holds feature flags
type featureFlagsConfig struct {
	scaleToZeroEnabled          bool
	limitedModeEnabled          bool
	scaleFromZeroMaxConcurrency int
}

// SaturationScalingConfigPerModel represents saturation scaling configuration
// for all models. Maps model ID (or "default" key) to its configuration.
type SaturationScalingConfigPerModel map[string]interfaces.SaturationScalingConfig

// saturationConfig holds saturation scaling configuration (namespace-aware)
type saturationConfig struct {
	// Global default configuration
	global SaturationScalingConfigPerModel

	// Namespace-local configuration overrides (keyed by namespace name)
	namespaceConfigs map[string]SaturationScalingConfigPerModel
}

// scaleToZeroConfig holds scale-to-zero configuration (namespace-aware)
type scaleToZeroConfig struct {
	// Global default configuration
	global ScaleToZeroConfigData

	// Namespace-local configuration overrides (keyed by namespace name)
	namespaceConfigs map[string]ScaleToZeroConfigData
}

// StaticConfig holds configuration that is immutable after startup.
// These settings are loaded once at startup and cannot be changed at runtime.
// EPPConfig holds EPP (Endpoint Pool) integration configuration.
type EPPConfig struct {
	// EPP metric reader bearer token for pod scraping
	MetricReaderBearerToken string // EPP_METRIC_READER_BEARER_TOKEN
}

// ============================================================================
// Infrastructure Getters (thread-safe)
// ============================================================================

// MetricsAddr returns the metrics bind address.
// Thread-safe.
func (c *Config) MetricsAddr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.metricsAddr
}

// ProbeAddr returns the health probe bind address.
// Thread-safe.
func (c *Config) ProbeAddr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.probeAddr
}

// EnableLeaderElection returns whether leader election is enabled.
// Thread-safe.
func (c *Config) EnableLeaderElection() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.enableLeaderElection
}

// LeaderElectionID returns the leader election ID.
// Thread-safe.
func (c *Config) LeaderElectionID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.leaderElectionID
}

// LeaseDuration returns the leader election lease duration.
// Thread-safe.
func (c *Config) LeaseDuration() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.leaseDuration
}

// RenewDeadline returns the leader election renew deadline.
// Thread-safe.
func (c *Config) RenewDeadline() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.renewDeadline
}

// RetryPeriod returns the leader election retry period.
// Thread-safe.
func (c *Config) RetryPeriod() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.retryPeriod
}

// RestTimeout returns the REST client timeout.
// Thread-safe.
func (c *Config) RestTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.restTimeout
}

// SecureMetrics returns whether metrics endpoint uses HTTPS.
// Thread-safe.
func (c *Config) SecureMetrics() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.secureMetrics
}

// EnableHTTP2 returns whether HTTP/2 is enabled.
// Thread-safe.
func (c *Config) EnableHTTP2() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.enableHTTP2
}

// WatchNamespace returns the namespace to watch (empty = all namespaces).
// Thread-safe.
func (c *Config) WatchNamespace() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.watchNamespace
}

// LoggerVerbosity returns the logger verbosity level.
// Thread-safe.
func (c *Config) LoggerVerbosity() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.infrastructure.loggerVerbosity
}

// ============================================================================
// TLS Getters (thread-safe)
// ============================================================================

// WebhookCertPath returns the webhook certificate path.
// Thread-safe.
func (c *Config) WebhookCertPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.webhookCertPath
}

// WebhookCertName returns the webhook certificate name.
// Thread-safe.
func (c *Config) WebhookCertName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.webhookCertName
}

// WebhookCertKey returns the webhook certificate key.
// Thread-safe.
func (c *Config) WebhookCertKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.webhookCertKey
}

// MetricsCertPath returns the metrics certificate path.
// Thread-safe.
func (c *Config) MetricsCertPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.metricsCertPath
}

// MetricsCertName returns the metrics certificate name.
// Thread-safe.
func (c *Config) MetricsCertName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.metricsCertName
}

// MetricsCertKey returns the metrics certificate key.
// Thread-safe.
func (c *Config) MetricsCertKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tls.metricsCertKey
}

// ============================================================================
// Prometheus Getters (thread-safe)
// ============================================================================

// PrometheusBaseURL returns the Prometheus base URL.
// Thread-safe.
func (c *Config) PrometheusBaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.baseURL
}

// PrometheusBearerToken returns the Prometheus bearer token.
// Thread-safe.
func (c *Config) PrometheusBearerToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.bearerToken
}

// PrometheusTokenPath returns the Prometheus token path.
// Thread-safe.
func (c *Config) PrometheusTokenPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.tokenPath
}

// PrometheusInsecureSkipVerify returns whether to skip TLS verification.
// Thread-safe.
func (c *Config) PrometheusInsecureSkipVerify() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.insecureSkipVerify
}

// PrometheusCACertPath returns the Prometheus CA certificate path.
// Thread-safe.
func (c *Config) PrometheusCACertPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.caCertPath
}

// PrometheusClientCertPath returns the Prometheus client certificate path.
// Thread-safe.
func (c *Config) PrometheusClientCertPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.clientCertPath
}

// PrometheusClientKeyPath returns the Prometheus client key path.
// Thread-safe.
func (c *Config) PrometheusClientKeyPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.clientKeyPath
}

// PrometheusServerName returns the Prometheus server name.
// Thread-safe.
func (c *Config) PrometheusServerName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prometheus.serverName
}

// PrometheusCacheConfig returns the current Prometheus cache configuration.
// Thread-safe. Returns a copy to prevent external modifications.
func (c *Config) PrometheusCacheConfig() *CacheConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.prometheus.cache == nil {
		return nil
	}
	// Return a copy to prevent external modifications
	cp := *c.prometheus.cache
	return &cp
}

// Prometheus returns the full Prometheus configuration as an interfaces.PrometheusConfig.
// Thread-safe. Returns a copy to prevent external modifications.
func (c *Config) Prometheus() *interfaces.PrometheusConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return &interfaces.PrometheusConfig{
		BaseURL:            c.prometheus.baseURL,
		BearerToken:        c.prometheus.bearerToken,
		TokenPath:          c.prometheus.tokenPath,
		InsecureSkipVerify: c.prometheus.insecureSkipVerify,
		CACertPath:         c.prometheus.caCertPath,
		ClientCertPath:     c.prometheus.clientCertPath,
		ClientKeyPath:      c.prometheus.clientKeyPath,
		ServerName:         c.prometheus.serverName,
	}
}

// ============================================================================
// EPP Getters (thread-safe)
// ============================================================================

// EPPMetricReaderBearerToken returns the EPP metric reader bearer token.
// Thread-safe.
func (c *Config) EPPMetricReaderBearerToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.epp.metricReaderBearerToken
}

// ============================================================================
// Optimization Getters (thread-safe)
// ============================================================================

// OptimizationInterval returns the current optimization interval.
// Thread-safe.
func (c *Config) OptimizationInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.optimization.interval
}

// ============================================================================
// Feature Flags Getters (thread-safe)
// ============================================================================

// ScaleToZeroEnabled returns whether scale-to-zero is enabled.
// Thread-safe.
func (c *Config) ScaleToZeroEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.features.scaleToZeroEnabled
}

// LimitedModeEnabled returns whether limited mode is enabled.
// Thread-safe.
func (c *Config) LimitedModeEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.features.limitedModeEnabled
}

// ScaleFromZeroMaxConcurrency returns the scale-from-zero max concurrency.
// Thread-safe.
func (c *Config) ScaleFromZeroMaxConcurrency() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.features.scaleFromZeroMaxConcurrency
}

// SaturationConfig returns the current global saturation scaling configuration.
// Thread-safe. Returns a copy to prevent external modifications.
// For namespace-aware lookups, use SaturationConfigForNamespace instead.
func (c *Config) SaturationConfig() map[string]interfaces.SaturationScalingConfig {
	return c.SaturationConfigForNamespace("")
}

// resolveSaturationConfig resolves saturation config for a namespace (namespace-local > global).
// Must be called while holding at least a read lock.
func (c *Config) resolveSaturationConfig(namespace string) map[string]interfaces.SaturationScalingConfig {
	// Check namespace-local first (if namespace is provided)
	if namespace != "" {
		if nsConfig, exists := c.saturation.namespaceConfigs[namespace]; exists {
			if len(nsConfig) > 0 {
				return nsConfig
			}
		}
	}

	// Fall back to global
	if len(c.saturation.global) > 0 {
		return c.saturation.global
	}

	return nil
}

// resolveScaleToZeroConfig resolves scale-to-zero config for a namespace (namespace-local > global).
// Must be called while holding at least a read lock.
func (c *Config) resolveScaleToZeroConfig(namespace string) ScaleToZeroConfigData {
	// Check namespace-local first (if namespace is provided)
	if namespace != "" {
		if nsConfig, exists := c.scaleToZero.namespaceConfigs[namespace]; exists {
			if len(nsConfig) > 0 {
				return nsConfig
			}
		}
	}

	// Fall back to global
	if len(c.scaleToZero.global) > 0 {
		return c.scaleToZero.global
	}

	return nil
}

// SaturationConfigForNamespace returns the saturation scaling configuration for the given namespace.
// Resolution order: namespace-local > global
// Thread-safe. Returns a copy to prevent external modifications.
// If namespace is empty, returns global config.
func (c *Config) SaturationConfigForNamespace(namespace string) map[string]interfaces.SaturationScalingConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sourceConfig := c.resolveSaturationConfig(namespace)
	return copySaturationConfig(sourceConfig)
}

// copySaturationConfig creates a deep copy of the saturation config map.
func copySaturationConfig(src map[string]interfaces.SaturationScalingConfig) map[string]interfaces.SaturationScalingConfig {
	if src == nil {
		return make(map[string]interfaces.SaturationScalingConfig)
	}
	result := make(map[string]interfaces.SaturationScalingConfig, len(src))
	for k, v := range src {
		result[k] = v
	}
	return result
}

// ScaleToZeroConfig returns the current global scale-to-zero configuration.
// Thread-safe.
// For namespace-aware lookups, use ScaleToZeroConfigForNamespace instead.
func (c *Config) ScaleToZeroConfig() ScaleToZeroConfigData {
	return c.ScaleToZeroConfigForNamespace("")
}

// ScaleToZeroConfigForNamespace returns the scale-to-zero configuration for the given namespace.
// Resolution order: namespace-local > global
// Thread-safe. Returns a copy to prevent external modifications.
// If namespace is empty, returns global config.
func (c *Config) ScaleToZeroConfigForNamespace(namespace string) ScaleToZeroConfigData {
	c.mu.RLock()
	defer c.mu.RUnlock()
	sourceConfig := c.resolveScaleToZeroConfig(namespace)
	return copyScaleToZeroConfig(sourceConfig)
}

// copyScaleToZeroConfig creates a deep copy of the scale-to-zero config map.
func copyScaleToZeroConfig(src ScaleToZeroConfigData) ScaleToZeroConfigData {
	if src == nil {
		return make(ScaleToZeroConfigData)
	}
	result := make(ScaleToZeroConfigData, len(src))
	for k, v := range src {
		result[k] = v
	}
	return result
}

// UpdateOptimizationInterval updates the optimization interval.
// Thread-safe.
func (c *Config) UpdateOptimizationInterval(interval time.Duration) {
	c.mu.Lock()
	oldInterval := c.optimization.interval
	c.optimization.interval = interval
	c.mu.Unlock()
	if oldInterval != interval {
		ctrl.Log.Info("Updated optimization interval", "old", oldInterval, "new", interval)
	}
}

// UpdateSaturationConfig updates the global saturation scaling configuration.
// Thread-safe. Takes a copy of the provided map to prevent external modifications.
// For namespace-local updates, use UpdateSaturationConfigForNamespace instead.
func (c *Config) UpdateSaturationConfig(config map[string]interfaces.SaturationScalingConfig) {
	c.UpdateSaturationConfigForNamespace("", config)
}

// UpdateSaturationConfigForNamespace updates the saturation scaling configuration for the given namespace.
// If namespace is empty, updates global config.
// Thread-safe. Takes a copy of the provided map to prevent external modifications.
func (c *Config) UpdateSaturationConfigForNamespace(namespace string, config map[string]interfaces.SaturationScalingConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Make a copy to prevent external modifications
	newConfig := make(map[string]interfaces.SaturationScalingConfig, len(config))
	for k, v := range config {
		newConfig[k] = v
	}

	var oldCount int
	if namespace == "" {
		// Update global
		oldCount = len(c.saturation.global)
		c.saturation.global = newConfig
		newCount := len(c.saturation.global)
		if oldCount != newCount {
			ctrl.Log.Info("Updated global saturation config", "oldEntries", oldCount, "newEntries", newCount)
		}
	} else {
		// Update namespace-local
		if c.saturation.namespaceConfigs == nil {
			c.saturation.namespaceConfigs = make(map[string]SaturationScalingConfigPerModel)
		}
		oldCount = len(c.saturation.namespaceConfigs[namespace])
		c.saturation.namespaceConfigs[namespace] = newConfig
		newCount := len(c.saturation.namespaceConfigs[namespace])
		if oldCount != newCount {
			ctrl.Log.Info("Updated namespace-local saturation config", "namespace", namespace, "oldEntries", oldCount, "newEntries", newCount)
		}
	}

}

// UpdateScaleToZeroConfig updates the global scale-to-zero configuration.
// Thread-safe. Takes a copy of the provided map to prevent external modifications.
// For namespace-local updates, use UpdateScaleToZeroConfigForNamespace instead.
func (c *Config) UpdateScaleToZeroConfig(config ScaleToZeroConfigData) {
	c.UpdateScaleToZeroConfigForNamespace("", config)
}

// UpdateScaleToZeroConfigForNamespace updates the scale-to-zero configuration for the given namespace.
// If namespace is empty, updates global config.
// Thread-safe. Takes a copy of the provided map to prevent external modifications.
func (c *Config) UpdateScaleToZeroConfigForNamespace(namespace string, config ScaleToZeroConfigData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Make a copy to prevent external modifications
	newConfig := make(ScaleToZeroConfigData, len(config))
	for k, v := range config {
		newConfig[k] = v
	}

	var oldCount int
	if namespace == "" {
		// Update global
		oldCount = len(c.scaleToZero.global)
		c.scaleToZero.global = newConfig
		newCount := len(c.scaleToZero.global)
		if oldCount != newCount {
			ctrl.Log.Info("Updated global scale-to-zero config", "oldModels", oldCount, "newModels", newCount)
		}
	} else {
		// Update namespace-local
		if c.scaleToZero.namespaceConfigs == nil {
			c.scaleToZero.namespaceConfigs = make(map[string]ScaleToZeroConfigData)
		}
		oldCount = len(c.scaleToZero.namespaceConfigs[namespace])
		c.scaleToZero.namespaceConfigs[namespace] = newConfig
		newCount := len(c.scaleToZero.namespaceConfigs[namespace])
		if oldCount != newCount {
			ctrl.Log.Info("Updated namespace-local scale-to-zero config", "namespace", namespace, "oldModels", oldCount, "newModels", newCount)
		}
	}

}

// RemoveNamespaceConfig removes the namespace-local configuration for the given namespace.
// This is called when a namespace-local ConfigMap is deleted, allowing fallback to global config.
// Thread-safe.
func (c *Config) RemoveNamespaceConfig(namespace string) {
	if namespace == "" {
		return // Don't remove global config
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := false
	if c.saturation.namespaceConfigs != nil {
		if _, exists := c.saturation.namespaceConfigs[namespace]; exists {
			delete(c.saturation.namespaceConfigs, namespace)
			removed = true
		}
	}
	if c.scaleToZero.namespaceConfigs != nil {
		if _, exists := c.scaleToZero.namespaceConfigs[namespace]; exists {
			delete(c.scaleToZero.namespaceConfigs, namespace)
			removed = true
		}
	}
	if removed {
		ctrl.Log.Info("Removed namespace-local config", "namespace", namespace)
	}
}

// UpdatePrometheusCacheConfig updates the Prometheus cache configuration.
// Thread-safe.
func (c *Config) UpdatePrometheusCacheConfig(cacheConfig *CacheConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cacheConfig == nil {
		c.prometheus.cache = nil
	} else {
		// Make a copy
		cp := *cacheConfig
		c.prometheus.cache = &cp
	}
}

// NewTestConfig creates a minimal Config for testing purposes.
// It provides sensible defaults for all required fields.
// This helper is intended for use in unit tests, integration tests, and e2e tests
// where a valid Config instance is needed but full configuration is not required.
// NOTE: This function is exported for testing purposes only and should not be used in production code.
func NewTestConfig() *Config {
	cfg := &Config{
		infrastructure: infrastructureConfig{
			metricsAddr:          "0",
			probeAddr:            ":8081",
			enableLeaderElection: false,
			leaderElectionID:     "test-election-id",
			leaseDuration:        60 * time.Second,
			renewDeadline:        50 * time.Second,
			retryPeriod:          10 * time.Second,
			restTimeout:          60 * time.Second,
			secureMetrics:        false,
			enableHTTP2:          false,
			watchNamespace:       "",
			loggerVerbosity:      0,
		},
		tls: tlsConfig{
			webhookCertName: "tls.crt",
			webhookCertKey:  "tls.key",
			metricsCertName: "tls.crt",
			metricsCertKey:  "tls.key",
		},
		optimization: optimizationConfig{
			interval: 60 * time.Second,
		},
		features: featureFlagsConfig{
			scaleToZeroEnabled:          false,
			limitedModeEnabled:          false,
			scaleFromZeroMaxConcurrency: 10,
		},
		saturation: saturationConfig{
			global:           make(SaturationScalingConfigPerModel),
			namespaceConfigs: make(map[string]SaturationScalingConfigPerModel),
		},
		scaleToZero: scaleToZeroConfig{
			global:           make(ScaleToZeroConfigData),
			namespaceConfigs: make(map[string]ScaleToZeroConfigData),
		},
	}
	return cfg
}

// NewTestConfigWithPrometheus creates a test Config with Prometheus configuration.
// This is a convenience helper for tests that need a Config with Prometheus already configured.
// It uses the public Load API internally, so it's safe for use in other packages.
func NewTestConfigWithPrometheus(ctx context.Context, prometheusURL string, k8sClient client.Client) (*Config, error) {
	// Set environment variable for Prometheus URL
	_ = os.Setenv("PROMETHEUS_BASE_URL", prometheusURL)
	defer func() { _ = os.Unsetenv("PROMETHEUS_BASE_URL") }()

	flags := StaticConfigFlags{}
	cfg, err := Load(ctx, flags, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create test config: %w", err)
	}
	return cfg, nil
}

// setPrometheusConfigForTesting sets the Prometheus configuration for testing purposes only.
// This is internal and can only be used by tests in the config package.
//
//nolint:unused // Used by tests in config_test.go
func (c *Config) setPrometheusConfigForTesting(promConfig *interfaces.PrometheusConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if promConfig != nil {
		c.prometheus.baseURL = promConfig.BaseURL
		c.prometheus.bearerToken = promConfig.BearerToken
		c.prometheus.tokenPath = promConfig.TokenPath
		c.prometheus.insecureSkipVerify = promConfig.InsecureSkipVerify
		c.prometheus.caCertPath = promConfig.CACertPath
		c.prometheus.clientCertPath = promConfig.ClientCertPath
		c.prometheus.clientKeyPath = promConfig.ClientKeyPath
		c.prometheus.serverName = promConfig.ServerName
	}
}

// setOptimizationIntervalForTesting sets the optimization interval for testing purposes only.
// This is internal and can only be used by tests in the config package.
//
//nolint:unused // Used by tests in config_test.go
func (c *Config) setOptimizationIntervalForTesting(interval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.optimization.interval = interval
}

// setPrometheusCacheConfigForTesting sets the Prometheus cache configuration for testing purposes only.
// This is internal and can only be used by tests in the config package.
//
//nolint:unused // Used by tests in config_test.go
func (c *Config) setPrometheusCacheConfigForTesting(cacheConfig *CacheConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cacheConfig == nil {
		c.prometheus.cache = nil
	} else {
		cp := *cacheConfig
		c.prometheus.cache = &cp
	}
}
