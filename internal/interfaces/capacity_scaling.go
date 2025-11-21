package interfaces

import "fmt"

// CapacityScalingConfig holds capacity-based scaling thresholds for a model variant.
// Capacity scaling is enabled by default and uses these thresholds to determine when
// replicas are saturated and when to scale up.
type CapacityScalingConfig struct {
	// ModelID is the model identifier (only used in override entries)
	ModelID string `yaml:"model_id,omitempty"`

	// Namespace is the namespace for this override (only used in override entries)
	Namespace string `yaml:"namespace,omitempty"`

	// KvCacheThreshold: Replica is saturated if KV cache utilization >= this threshold (0.0-1.0)
	KvCacheThreshold float64 `yaml:"kvCacheThreshold"`

	// QueueLengthThreshold: Replica is saturated if queue length >= this threshold
	QueueLengthThreshold float64 `yaml:"queueLengthThreshold"`

	// KvSpareTrigger: Scale-up if average spare KV cache capacity < this value (0.0-1.0)
	KvSpareTrigger float64 `yaml:"kvSpareTrigger"`

	// QueueSpareTrigger: Scale-up if average spare queue capacity < this value
	QueueSpareTrigger float64 `yaml:"queueSpareTrigger"`
}

// DefaultCapacityScalingConfig returns hardcoded default configuration.
// Used as fallback when ConfigMap is missing or has no 'default' entry.
func DefaultCapacityScalingConfig() CapacityScalingConfig {
	return CapacityScalingConfig{
		KvCacheThreshold:     0.80,
		QueueLengthThreshold: 5,
		KvSpareTrigger:       0.10,
		QueueSpareTrigger:    3,
	}
}

// Merge applies non-zero values from override on top of base config.
// This allows partial overrides where unspecified fields inherit from default.
// Note: model_id and namespace are not merged (they're metadata fields).
func (base *CapacityScalingConfig) Merge(override CapacityScalingConfig) {
	if override.KvCacheThreshold != 0 {
		base.KvCacheThreshold = override.KvCacheThreshold
	}
	if override.QueueLengthThreshold != 0 {
		base.QueueLengthThreshold = override.QueueLengthThreshold
	}
	if override.KvSpareTrigger != 0 {
		base.KvSpareTrigger = override.KvSpareTrigger
	}
	if override.QueueSpareTrigger != 0 {
		base.QueueSpareTrigger = override.QueueSpareTrigger
	}
}

// Validate checks for invalid threshold values.
// Returns error with descriptive message if validation fails.
func (c *CapacityScalingConfig) Validate() error {
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
	// KV cache threshold should be greater than spare trigger (otherwise contradictory)
	if c.KvCacheThreshold < c.KvSpareTrigger {
		return fmt.Errorf("kvCacheThreshold (%.2f) should be >= kvSpareTrigger (%.2f)",
			c.KvCacheThreshold, c.KvSpareTrigger)
	}
	// Queue threshold should be greater than spare trigger (same logic as KV cache)
	if c.QueueLengthThreshold < c.QueueSpareTrigger {
		return fmt.Errorf("queueLengthThreshold (%.1f) should be >= queueSpareTrigger (%.1f)",
			c.QueueLengthThreshold, c.QueueSpareTrigger)
	}
	return nil
}
