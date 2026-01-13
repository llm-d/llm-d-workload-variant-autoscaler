package config

import (
	"fmt"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/logging"
)

// Unified model scaling configuration constants
const (
	// DefaultModelScalingConfigMapName is the default name of the ConfigMap that stores
	// per-model scaling configuration (saturation thresholds + scale-to-zero settings).
	DefaultModelScalingConfigMapName = "model-scaling-config"
)

// ModelScalingConfig represents the unified scaling configuration for a single model.
// It combines saturation-based scaling thresholds with scale-to-zero settings.
type ModelScalingConfig struct {
	// ModelID is the model identifier (only used in override entries)
	ModelID string `yaml:"model_id,omitempty" json:"model_id,omitempty"`

	// Namespace is the namespace for this override (only used in override entries)
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// Saturation scaling thresholds
	// KvCacheThreshold: Replica is saturated if KV cache utilization >= this threshold (0.0-1.0)
	KvCacheThreshold float64 `yaml:"kvCacheThreshold,omitempty" json:"kvCacheThreshold,omitempty"`

	// QueueLengthThreshold: Replica is saturated if queue length >= this threshold
	QueueLengthThreshold float64 `yaml:"queueLengthThreshold,omitempty" json:"queueLengthThreshold,omitempty"`

	// KvSpareTrigger: Scale-up if average spare KV cache capacity < this value (0.0-1.0)
	KvSpareTrigger float64 `yaml:"kvSpareTrigger,omitempty" json:"kvSpareTrigger,omitempty"`

	// QueueSpareTrigger: Scale-up if average spare queue capacity < this value
	QueueSpareTrigger float64 `yaml:"queueSpareTrigger,omitempty" json:"queueSpareTrigger,omitempty"`

	// Scale-to-zero settings
	// EnableScaleToZero enables scaling the model to zero replicas when there is no traffic.
	// Use pointer to allow omitting this field and inheriting from global defaults.
	EnableScaleToZero *bool `yaml:"enableScaleToZero,omitempty" json:"enableScaleToZero,omitempty"`

	// ScaleToZeroRetentionPeriod specifies how long to wait after the last request before scaling to zero.
	// This is stored as a string duration (e.g., "5m", "1h", "30s").
	ScaleToZeroRetentionPeriod string `yaml:"scaleToZeroRetentionPeriod,omitempty" json:"scaleToZeroRetentionPeriod,omitempty"`
}

// ModelScalingConfigData holds pre-read scaling configuration data for all models.
// Maps model ID to its configuration.
type ModelScalingConfigData map[string]ModelScalingConfig

// Validate checks for invalid configuration values.
func (c *ModelScalingConfig) Validate() error {
	if c.KvCacheThreshold < 0 || c.KvCacheThreshold > 1 {
		return fmt.Errorf("kvCacheThreshold must be between 0 and 1, got %.2f", c.KvCacheThreshold)
	}
	if c.QueueLengthThreshold < 0 {
		return fmt.Errorf("queueLengthThreshold must be >= 0, got %.1f", c.QueueLengthThreshold)
	}
	if c.KvSpareTrigger < 0 || c.KvSpareTrigger > 1 {
		return fmt.Errorf("kvSpareTrigger must be between 0 and 1, got %.2f", c.KvSpareTrigger)
	}
	if c.QueueSpareTrigger < 0 {
		return fmt.Errorf("queueSpareTrigger must be >= 0, got %.1f", c.QueueSpareTrigger)
	}
	if c.KvCacheThreshold != 0 && c.KvSpareTrigger != 0 && c.KvCacheThreshold < c.KvSpareTrigger {
		return fmt.Errorf("kvCacheThreshold (%.2f) should be >= kvSpareTrigger (%.2f)",
			c.KvCacheThreshold, c.KvSpareTrigger)
	}
	if c.ScaleToZeroRetentionPeriod != "" {
		if _, err := ValidateRetentionPeriod(c.ScaleToZeroRetentionPeriod); err != nil {
			return fmt.Errorf("invalid retentionPeriod: %w", err)
		}
	}
	return nil
}

// ParseModelScalingConfigMap parses unified scaling configuration from a ConfigMap's data.
// The ConfigMap format:
//   - "default": global defaults for all models
//   - "<override-name>": per-model configuration with model_id field
func ParseModelScalingConfigMap(data map[string]string) ModelScalingConfigData {
	if data == nil {
		return make(ModelScalingConfigData)
	}

	out := make(ModelScalingConfigData)
	modelIDToKeys := make(map[string][]string)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		configStr := data[key]

		var config ModelScalingConfig
		if err := yaml.Unmarshal([]byte(configStr), &config); err != nil {
			ctrl.Log.Info("Failed to parse model scaling config entry, skipping",
				"key", key,
				"error", err)
			continue
		}

		if err := config.Validate(); err != nil {
			ctrl.Log.Info("Invalid model scaling config entry, skipping",
				"key", key,
				"error", err)
			continue
		}

		// Handle global defaults
		if key == GlobalDefaultsKey {
			out[GlobalDefaultsKey] = config
			continue
		}

		// Handle per-model overrides
		if config.ModelID == "" {
			ctrl.Log.Info("Skipping model scaling config without model_id field",
				"key", key)
			continue
		}

		if existingKeys, exists := modelIDToKeys[config.ModelID]; exists {
			ctrl.Log.Info("Duplicate model_id found in model-scaling ConfigMap - first key wins",
				"model_id", config.ModelID,
				"winningKey", existingKeys[0],
				"duplicateKey", key)
			continue
		}
		modelIDToKeys[config.ModelID] = append(modelIDToKeys[config.ModelID], key)

		out[config.ModelID] = config
	}

	ctrl.Log.V(logging.DEBUG).Info("Parsed model scaling config",
		"modelCount", len(out))

	return out
}

// GetModelConfig returns the effective configuration for a specific model.
// It merges the model-specific config with global defaults.
func (data ModelScalingConfigData) GetModelConfig(modelID string) ModelScalingConfig {
	defaults := data[GlobalDefaultsKey]
	modelConfig, hasModel := data[modelID]

	if !hasModel {
		return defaults
	}

	// Merge: model-specific values override defaults
	result := defaults

	if modelConfig.ModelID != "" {
		result.ModelID = modelConfig.ModelID
	}
	if modelConfig.Namespace != "" {
		result.Namespace = modelConfig.Namespace
	}
	if modelConfig.KvCacheThreshold != 0 {
		result.KvCacheThreshold = modelConfig.KvCacheThreshold
	}
	if modelConfig.QueueLengthThreshold != 0 {
		result.QueueLengthThreshold = modelConfig.QueueLengthThreshold
	}
	if modelConfig.KvSpareTrigger != 0 {
		result.KvSpareTrigger = modelConfig.KvSpareTrigger
	}
	if modelConfig.QueueSpareTrigger != 0 {
		result.QueueSpareTrigger = modelConfig.QueueSpareTrigger
	}
	if modelConfig.EnableScaleToZero != nil {
		result.EnableScaleToZero = modelConfig.EnableScaleToZero
	}
	if modelConfig.ScaleToZeroRetentionPeriod != "" {
		result.ScaleToZeroRetentionPeriod = modelConfig.ScaleToZeroRetentionPeriod
	}

	return result
}

// IsScaleToZeroEnabledForModel determines if scale-to-zero is enabled for a specific model.
func (data ModelScalingConfigData) IsScaleToZeroEnabledForModel(modelID string) bool {
	config := data.GetModelConfig(modelID)
	if config.EnableScaleToZero != nil {
		return *config.EnableScaleToZero
	}
	return false
}

// GetRetentionPeriodForModel returns the retention period for a specific model.
func (data ModelScalingConfigData) GetRetentionPeriodForModel(modelID string) time.Duration {
	config := data.GetModelConfig(modelID)
	if config.ScaleToZeroRetentionPeriod != "" {
		duration, err := ValidateRetentionPeriod(config.ScaleToZeroRetentionPeriod)
		if err == nil {
			return duration
		}
	}
	return DefaultScaleToZeroRetentionPeriod
}

// GetMinReplicasForModel returns the minimum number of replicas for a model.
// Returns 0 if scale-to-zero is enabled, otherwise returns 1.
func (data ModelScalingConfigData) GetMinReplicasForModel(modelID string) int {
	if data.IsScaleToZeroEnabledForModel(modelID) {
		return 0
	}
	return 1
}

// GetSaturationThresholds returns saturation scaling thresholds for a specific model.
// This provides backwards compatibility with the SaturationScalingConfig interface.
func (data ModelScalingConfigData) GetSaturationThresholds(modelID string) (kvCacheThreshold, queueLengthThreshold, kvSpareTrigger, queueSpareTrigger float64) {
	config := data.GetModelConfig(modelID)
	return config.KvCacheThreshold, config.QueueLengthThreshold, config.KvSpareTrigger, config.QueueSpareTrigger
}

// HasSaturationConfig returns true if the unified config has any saturation thresholds configured.
func (data ModelScalingConfigData) HasSaturationConfig() bool {
	if len(data) == 0 {
		return false
	}
	// Check if default config has any saturation thresholds
	if defaults, ok := data[GlobalDefaultsKey]; ok {
		return defaults.KvCacheThreshold > 0 || defaults.QueueLengthThreshold > 0 ||
			defaults.KvSpareTrigger > 0 || defaults.QueueSpareTrigger > 0
	}
	return false
}

// ToSaturationConfig converts ModelScalingConfigData to the SaturationScalingConfig map format
// used by the saturation engine.
func (data ModelScalingConfigData) ToSaturationConfig() map[string]interfaces.SaturationScalingConfig {
	result := make(map[string]interfaces.SaturationScalingConfig)

	for key, config := range data {
		result[key] = interfaces.SaturationScalingConfig{
			ModelID:              config.ModelID,
			Namespace:            config.Namespace,
			KvCacheThreshold:     config.KvCacheThreshold,
			QueueLengthThreshold: config.QueueLengthThreshold,
			KvSpareTrigger:       config.KvSpareTrigger,
			QueueSpareTrigger:    config.QueueSpareTrigger,
		}
	}

	return result
}

// ToScaleToZeroConfig converts ModelScalingConfigData to the ScaleToZeroConfigData format.
func (data ModelScalingConfigData) ToScaleToZeroConfig() ScaleToZeroConfigData {
	result := make(ScaleToZeroConfigData)

	for key, config := range data {
		result[key] = ModelScaleToZeroConfig{
			ModelID:           config.ModelID,
			Namespace:         config.Namespace,
			EnableScaleToZero: config.EnableScaleToZero,
			RetentionPeriod:   config.ScaleToZeroRetentionPeriod,
		}
	}

	return result
}
