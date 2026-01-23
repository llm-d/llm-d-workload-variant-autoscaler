package interfaces

import "fmt"

// SaturationScalingConfig holds saturation-based scaling thresholds for a model variant.
// Saturation scaling is enabled by default and uses these thresholds to determine when
// replicas are saturated and when to scale up.
type SaturationScalingConfig struct {
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

// Validate checks for invalid threshold values.
// Returns error with descriptive message if validation fails.
func (c *SaturationScalingConfig) Validate() error {
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
	return nil
}
