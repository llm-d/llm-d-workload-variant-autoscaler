package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/utils"
)

// StaticConfigFlags holds CLI flag values for static configuration.
// This is passed from main.go to Load() function.
// Boolean and duration flags use pointers to distinguish between "unset" (nil) and "explicitly set" (false/true or 0/non-zero).
// This allows proper precedence: flag > env > ConfigMap.
type StaticConfigFlags struct {
	MetricsAddr          string
	ProbeAddr            string
	EnableLeaderElection *bool // nil = unset, false/true = explicitly set
	LeaderElectionID     string
	LeaseDuration        *time.Duration // nil = unset, duration = explicitly set (even if 0)
	RenewDeadline        *time.Duration // nil = unset, duration = explicitly set (even if 0)
	RetryPeriod          *time.Duration // nil = unset, duration = explicitly set (even if 0)
	RestTimeout          *time.Duration // nil = unset, duration = explicitly set (even if 0)
	SecureMetrics        *bool          // nil = unset, false/true = explicitly set
	EnableHTTP2          *bool          // nil = unset, false/true = explicitly set
	WatchNamespace       string
	LoggerVerbosity      int
	WebhookCertPath      string
	WebhookCertName      string
	WebhookCertKey       string
	MetricsCertPath      string
	MetricsCertName      string
	MetricsCertKey       string
}

const (
	// defaultOptimizationInterval is the default optimization interval used when
	// GLOBAL_OPT_INTERVAL is not specified in the ConfigMap.
	defaultOptimizationInterval = 60 * time.Second
)

// Load loads and validates the unified configuration.
// Precedence: flags > env > ConfigMap > defaults
// Returns error if required configuration is missing or invalid (fail-fast).
func Load(ctx context.Context, flags StaticConfigFlags, k8sClient client.Client) (*Config, error) {
	cfg := &Config{}

	// Load configuration (flags > env > ConfigMap > defaults)
	if err := loadConfig(ctx, cfg, flags, k8sClient); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// 3. Validate required configuration (fail-fast)
	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	ctrl.Log.Info("Configuration loaded successfully")
	return cfg, nil
}

// loadConfig loads configuration with precedence: flags > env > ConfigMap > defaults
func loadConfig(ctx context.Context, cfg *Config, flags StaticConfigFlags, k8sClient client.Client) error {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	// Initialize with defaults
	cfg.infrastructure = infrastructureConfig{
		metricsAddr:          "0", // Disable by default
		probeAddr:            ":8081",
		enableLeaderElection: false,
		leaderElectionID:     "72dd1cf1.llm-d.ai",
		leaseDuration:        60 * time.Second,
		renewDeadline:        50 * time.Second,
		retryPeriod:          10 * time.Second,
		restTimeout:          60 * time.Second,
		secureMetrics:        true,
		enableHTTP2:          false,
		watchNamespace:       "",
		loggerVerbosity:      0,
	}

	cfg.tls = tlsConfig{
		webhookCertName: "tls.crt",
		webhookCertKey:  "tls.key",
		metricsCertName: "tls.crt",
		metricsCertKey:  "tls.key",
	}

	cfg.optimization = optimizationConfig{
		interval: defaultOptimizationInterval,
	}

	cfg.features = featureFlagsConfig{
		scaleToZeroEnabled:          false,
		limitedModeEnabled:          false,
		scaleFromZeroMaxConcurrency: 10,
	}

	cfg.saturation = saturationConfig{
		global:           make(SaturationScalingConfigPerModel),
		namespaceConfigs: make(map[string]SaturationScalingConfigPerModel),
	}

	cfg.scaleToZero = scaleToZeroConfig{
		global:           make(ScaleToZeroConfigData),
		namespaceConfigs: make(map[string]ScaleToZeroConfigData),
	}

	cfg.prometheus.cache = defaultPrometheusCacheConfig()

	// Load from ConfigMap (if available)
	var cmData map[string]string
	cm := &corev1.ConfigMap{}
	cmName := ConfigMapName()
	cmNamespace := SystemNamespace()
	if err := utils.GetConfigMapWithBackoff(ctx, k8sClient, cmName, cmNamespace, cm); err == nil {
		cmData = cm.Data
		ctrl.Log.Info("Loaded ConfigMap for config", "name", cmName, "namespace", cmNamespace)
	} else {
		ctrl.Log.Info("ConfigMap not found, using defaults and flags", "name", cmName, "namespace", cmNamespace, "error", err)
		cmData = make(map[string]string)
	}

	// Apply precedence: flags > env > ConfigMap > defaults
	// Infrastructure settings
	cfg.infrastructure.metricsAddr = getStringValue(flags.MetricsAddr, os.Getenv("METRICS_BIND_ADDRESS"), cmData["METRICS_BIND_ADDRESS"], cfg.infrastructure.metricsAddr)
	cfg.infrastructure.probeAddr = getStringValue(flags.ProbeAddr, os.Getenv("HEALTH_PROBE_BIND_ADDRESS"), cmData["HEALTH_PROBE_BIND_ADDRESS"], cfg.infrastructure.probeAddr)
	cfg.infrastructure.enableLeaderElection = getBoolValue(flags.EnableLeaderElection, parseBoolEnv("LEADER_ELECT"), ParseBoolFromConfig(cmData, "LEADER_ELECT", cfg.infrastructure.enableLeaderElection))
	cfg.infrastructure.leaderElectionID = getStringValue(flags.LeaderElectionID, os.Getenv("LEADER_ELECTION_ID"), cmData["LEADER_ELECTION_ID"], cfg.infrastructure.leaderElectionID)

	// Parse duration from ConfigMap
	leaseDurationCM, _ := parseDurationFromConfigWithExists(cmData, "LEADER_ELECTION_LEASE_DURATION")
	renewDeadlineCM, _ := parseDurationFromConfigWithExists(cmData, "LEADER_ELECTION_RENEW_DEADLINE")
	retryPeriodCM, _ := parseDurationFromConfigWithExists(cmData, "LEADER_ELECTION_RETRY_PERIOD")
	restTimeoutCM, _ := parseDurationFromConfigWithExists(cmData, "REST_CLIENT_TIMEOUT")

	cfg.infrastructure.leaseDuration = getDurationValue(flags.LeaseDuration, parseDurationEnv("LEADER_ELECTION_LEASE_DURATION"), leaseDurationCM, cfg.infrastructure.leaseDuration)
	cfg.infrastructure.renewDeadline = getDurationValue(flags.RenewDeadline, parseDurationEnv("LEADER_ELECTION_RENEW_DEADLINE"), renewDeadlineCM, cfg.infrastructure.renewDeadline)
	cfg.infrastructure.retryPeriod = getDurationValue(flags.RetryPeriod, parseDurationEnv("LEADER_ELECTION_RETRY_PERIOD"), retryPeriodCM, cfg.infrastructure.retryPeriod)
	cfg.infrastructure.restTimeout = getDurationValue(flags.RestTimeout, parseDurationEnv("REST_CLIENT_TIMEOUT"), restTimeoutCM, cfg.infrastructure.restTimeout)
	cfg.infrastructure.secureMetrics = getBoolValue(flags.SecureMetrics, parseBoolEnv("METRICS_SECURE"), ParseBoolFromConfig(cmData, "METRICS_SECURE", cfg.infrastructure.secureMetrics))
	cfg.infrastructure.enableHTTP2 = getBoolValue(flags.EnableHTTP2, parseBoolEnv("ENABLE_HTTP2"), ParseBoolFromConfig(cmData, "ENABLE_HTTP2", cfg.infrastructure.enableHTTP2))
	cfg.infrastructure.watchNamespace = getStringValue(flags.WatchNamespace, os.Getenv("WATCH_NAMESPACE"), cmData["WATCH_NAMESPACE"], cfg.infrastructure.watchNamespace)
	cfg.infrastructure.loggerVerbosity = getIntValue(flags.LoggerVerbosity, parseIntEnv("V"), ParseIntFromConfig(cmData, "V", cfg.infrastructure.loggerVerbosity, 0))

	// TLS settings
	cfg.tls.webhookCertPath = getStringValue(flags.WebhookCertPath, os.Getenv("WEBHOOK_CERT_PATH"), cmData["WEBHOOK_CERT_PATH"], cfg.tls.webhookCertPath)
	cfg.tls.webhookCertName = getStringValue(flags.WebhookCertName, os.Getenv("WEBHOOK_CERT_NAME"), cmData["WEBHOOK_CERT_NAME"], cfg.tls.webhookCertName)
	cfg.tls.webhookCertKey = getStringValue(flags.WebhookCertKey, os.Getenv("WEBHOOK_CERT_KEY"), cmData["WEBHOOK_CERT_KEY"], cfg.tls.webhookCertKey)
	cfg.tls.metricsCertPath = getStringValue(flags.MetricsCertPath, os.Getenv("METRICS_CERT_PATH"), cmData["METRICS_CERT_PATH"], cfg.tls.metricsCertPath)
	cfg.tls.metricsCertName = getStringValue(flags.MetricsCertName, os.Getenv("METRICS_CERT_NAME"), cmData["METRICS_CERT_NAME"], cfg.tls.metricsCertName)
	cfg.tls.metricsCertKey = getStringValue(flags.MetricsCertKey, os.Getenv("METRICS_CERT_KEY"), cmData["METRICS_CERT_KEY"], cfg.tls.metricsCertKey)

	// Load Prometheus config (required) - env > ConfigMap
	promConfig, err := PrometheusConfig(ctx, k8sClient)
	if err != nil {
		return fmt.Errorf("failed to load Prometheus config: %w", err)
	}
	if promConfig == nil {
		return fmt.Errorf("prometheus configuration is required but not found. set PROMETHEUS_BASE_URL environment variable or configure via ConfigMap")
	}
	cfg.prometheus.baseURL = promConfig.BaseURL
	cfg.prometheus.bearerToken = promConfig.BearerToken
	cfg.prometheus.tokenPath = promConfig.TokenPath
	cfg.prometheus.insecureSkipVerify = promConfig.InsecureSkipVerify
	cfg.prometheus.caCertPath = promConfig.CACertPath
	cfg.prometheus.clientCertPath = promConfig.ClientCertPath
	cfg.prometheus.clientKeyPath = promConfig.ClientKeyPath
	cfg.prometheus.serverName = promConfig.ServerName

	// Load feature flags from ConfigMap
	cfg.features.scaleToZeroEnabled = ParseBoolFromConfig(cmData, "WVA_SCALE_TO_ZERO", false) ||
		strings.EqualFold(os.Getenv("WVA_SCALE_TO_ZERO"), "true")
	cfg.features.limitedModeEnabled = ParseBoolFromConfig(cmData, "WVA_LIMITED_MODE", false)
	cfg.features.scaleFromZeroMaxConcurrency = ParseIntFromConfig(cmData, "SCALE_FROM_ZERO_ENGINE_MAX_CONCURRENCY", 10, 1)

	// EPP configuration
	cfg.epp.metricReaderBearerToken = getStringValue("", os.Getenv("EPP_METRIC_READER_BEARER_TOKEN"), cmData["EPP_METRIC_READER_BEARER_TOKEN"], "")

	// Load dynamic config
	return loadDynamicConfigNew(ctx, cfg, k8sClient)
}

// loadDynamicConfigNew loads dynamic configuration
func loadDynamicConfigNew(ctx context.Context, cfg *Config, k8sClient client.Client) error {
	// Load optimization interval
	cm := &corev1.ConfigMap{}
	cmName := ConfigMapName()
	cmNamespace := SystemNamespace()
	if err := utils.GetConfigMapWithBackoff(ctx, k8sClient, cmName, cmNamespace, cm); err == nil {
		if intervalStr, ok := cm.Data["GLOBAL_OPT_INTERVAL"]; ok {
			if interval, err := time.ParseDuration(intervalStr); err == nil {
				cfg.optimization.interval = interval
			} else {
				ctrl.Log.Info("Invalid GLOBAL_OPT_INTERVAL, using default", "value", intervalStr, "error", err)
			}
		}
	}

	// Load Prometheus cache config
	if cacheConfig, err := ReadPrometheusCacheConfig(ctx, k8sClient); err == nil && cacheConfig != nil {
		cfg.prometheus.cache = cacheConfig
	}

	// Load scale-to-zero config (global)
	scaleToZeroCM := &corev1.ConfigMap{}
	scaleToZeroCMName := DefaultScaleToZeroConfigMapName
	if err := utils.GetConfigMapWithBackoff(ctx, k8sClient, scaleToZeroCMName, cmNamespace, scaleToZeroCM); err == nil {
		cfg.scaleToZero.global = ParseScaleToZeroConfigMap(scaleToZeroCM.Data)
	}

	// Load saturation scaling config (global)
	saturationCM := &corev1.ConfigMap{}
	saturationCMName := SaturationConfigMapName()
	if err := utils.GetConfigMapWithBackoff(ctx, k8sClient, saturationCMName, cmNamespace, saturationCM); err == nil {
		configs := make(map[string]interfaces.SaturationScalingConfig)
		for key, yamlStr := range saturationCM.Data {
			var satConfig interfaces.SaturationScalingConfig
			if err := yaml.Unmarshal([]byte(yamlStr), &satConfig); err != nil {
				ctrl.Log.Info("Failed to parse saturation scaling config entry", "key", key, "error", err)
				continue
			}
			// Validate
			if err := satConfig.Validate(); err != nil {
				ctrl.Log.Info("Invalid saturation scaling config entry", "key", key, "error", err)
				continue
			}
			configs[key] = satConfig
		}
		if len(configs) > 0 {
			cfg.saturation.global = configs
			ctrl.Log.Info("Loaded saturation scaling config", "entries", len(configs))
		}
	}

	return nil
}

// Helper functions for precedence resolution

func getStringValue(flag, env, cm, def string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	if cm != "" {
		return cm
	}
	return def
}

// getBoolValue implements precedence: flag > env > cm
// flag: *bool (nil = unset, false/true = explicitly set)
// env: *bool (nil = unset, false/true = explicitly set)
// cm: bool (ConfigMap value or default)
func getBoolValue(flag *bool, env *bool, cm bool) bool {
	// Precedence: flag > env > cm
	if flag != nil {
		return *flag
	}
	if env != nil {
		return *env
	}
	return cm
}

// getDurationValue implements precedence: flag > env > cm > defaultValue
// flag: *time.Duration (nil = unset, duration = explicitly set, even if 0 or negative)
// env: *time.Duration (nil = unset, duration = explicitly set, even if 0 or negative)
// cm: *time.Duration (nil = key not in ConfigMap or invalid, duration = explicitly set, even if 0 or negative)
// defaultValue: fallback when all are unset
func getDurationValue(flag *time.Duration, env *time.Duration, cm *time.Duration, defaultValue time.Duration) time.Duration {
	// Precedence: flag > env > cm > defaultValue
	if flag != nil {
		return *flag // Use flag even if it's 0 or negative
	}
	if env != nil {
		return *env // Use env even if it's 0 or negative
	}
	if cm != nil {
		return *cm // Use cm even if it's 0 or negative
	}
	return defaultValue
}

// parseDurationFromConfigWithExists parses a duration from ConfigMap and returns whether the key existed.
// Returns (nil, false) if key doesn't exist or value is invalid.
// Returns (&duration, true) if key exists and value is valid (even if 0 or negative).
func parseDurationFromConfigWithExists(data map[string]string, key string) (*time.Duration, bool) {
	if valStr, exists := data[key]; exists && valStr != "" {
		if d, err := time.ParseDuration(valStr); err == nil {
			return &d, true
		}
		ctrl.Log.Info("Invalid duration value in ConfigMap, treating as unset", "value", valStr, "key", key)
	}
	return nil, false
}

func getIntValue(flag int, env *int, cm int) int {
	if env != nil {
		return *env
	}
	if cm != 0 {
		return cm
	}
	return flag
}

// Helper functions for parsing env vars

func parseBoolEnv(key string) *bool {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	result := val == "true" || val == "1" || val == "yes"
	return &result
}

func parseDurationEnv(key string) *time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	if d, err := time.ParseDuration(val); err == nil {
		return &d
	}
	return nil
}

func parseIntEnv(key string) *int {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	var result int
	if _, err := fmt.Sscanf(val, "%d", &result); err == nil {
		return &result
	}
	return nil
}

// defaultPrometheusCacheConfig returns default Prometheus cache configuration
func defaultPrometheusCacheConfig() *CacheConfig {
	return &CacheConfig{
		Enabled:             true,
		TTL:                 30 * time.Second,
		CleanupInterval:     1 * time.Minute,
		FetchInterval:       30 * time.Second,
		FreshnessThresholds: DefaultFreshnessThresholds(),
	}
}
