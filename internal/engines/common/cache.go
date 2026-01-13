package common

import (
	"sync"
	"time"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/config"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// InternalDecisionCache holds the latest saturation decisions for VAs.
// This is used to pass decisions from the Engine to the Controller without API server interaction.
type InternalDecisionCache struct {
	sync.RWMutex
	items map[string]interfaces.VariantDecision
}

// Key format: namespace/name
func cacheKey(name, namespace string) string {
	return namespace + "/" + name
}

func (c *InternalDecisionCache) Set(name, namespace string, d interfaces.VariantDecision) {
	c.Lock()
	defer c.Unlock()
	key := cacheKey(name, namespace)
	c.items[key] = d
}

func (c *InternalDecisionCache) Get(name, namespace string) (interfaces.VariantDecision, bool) {
	c.RLock()
	defer c.RUnlock()
	key := cacheKey(name, namespace)
	val, ok := c.items[key]
	return val, ok
}

// Global cache instance
var DecisionCache = &InternalDecisionCache{
	items: make(map[string]interfaces.VariantDecision),
}

// DecisionTrigger is a channel to trigger reconciliation for VAs.
// Buffered to prevent blocking the engine loop.
var DecisionTrigger = make(chan event.GenericEvent, 1000)

// Helper to convert VariantDecision to OptimizedAlloc status
func DecisionToOptimizedAlloc(d interfaces.VariantDecision) (int, string, metav1.Time) {
	// If LastRunTime is adding to VariantDecision, use it, else Now
	// For now we assume the consumer sets LastRunTime or uses Now
	return d.TargetReplicas, d.AcceleratorName, metav1.NewTime(time.Now())
}

// GlobalConfig holds the shared configuration for the autoscaler components.
type GlobalConfig struct {
	sync.RWMutex
	OptimizationInterval string
	ModelScalingConfig   config.ModelScalingConfigData // Unified model scaling config
}

// UpdateOptimizationConfig updates the optimization interval.
func (c *GlobalConfig) UpdateOptimizationConfig(interval string) {
	c.Lock()
	defer c.Unlock()
	c.OptimizationInterval = interval
}

// GetOptimizationInterval returns the current optimization interval.
func (c *GlobalConfig) GetOptimizationInterval() string {
	c.RLock()
	defer c.RUnlock()
	return c.OptimizationInterval
}

// UpdateModelScalingConfig updates the unified model scaling configuration.
func (c *GlobalConfig) UpdateModelScalingConfig(configData config.ModelScalingConfigData) {
	c.Lock()
	defer c.Unlock()
	c.ModelScalingConfig = configData
}

// GetModelScalingConfig returns the current unified model scaling configuration.
func (c *GlobalConfig) GetModelScalingConfig() config.ModelScalingConfigData {
	c.RLock()
	defer c.RUnlock()
	if c.ModelScalingConfig == nil {
		return make(config.ModelScalingConfigData)
	}
	return c.ModelScalingConfig
}

// TransformationConfig is the global singleton for configuration.
// (Using name TransformationConfig as a placeholder/legacy name if suitable, or just Config)
var Config = &GlobalConfig{}
