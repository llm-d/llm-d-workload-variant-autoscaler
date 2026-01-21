package common

import (
	"sync"
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/config"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

func TestInternalDecisionCache(t *testing.T) {
	cache := &InternalDecisionCache{
		items: make(map[string]interfaces.VariantDecision),
	}

	// Test Set and Get
	decision := interfaces.VariantDecision{
		VariantName:     "test-variant",
		Namespace:       "test-ns",
		TargetReplicas:  5,
		AcceleratorName: "A100",
		Action:          interfaces.ActionScaleUp,
	}

	cache.Set("test-variant", "test-ns", decision)

	retrieved, ok := cache.Get("test-variant", "test-ns")
	if !ok {
		t.Error("Expected decision to be found in cache")
	}
	if retrieved.TargetReplicas != 5 {
		t.Errorf("Expected TargetReplicas 5, got %d", retrieved.TargetReplicas)
	}

	_, ok = cache.Get("non-existent", "test-ns")
	if ok {
		t.Error("Expected non-existent item to not be found")
	}

	// Test Concurrency
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Set("test-variant", "test-ns", decision)
			cache.Get("test-variant", "test-ns")
		}()
	}
	wg.Wait()
}

func TestGlobalConfig(t *testing.T) {
	globalConfig := &GlobalConfig{}

	// Test Optimization Config
	globalConfig.UpdateOptimizationConfig("60s")
	if globalConfig.GetOptimizationInterval() != "60s" {
		t.Errorf("Expected interval '60s', got '%s'", globalConfig.GetOptimizationInterval())
	}

	// Test Model Scaling Config
	modelScalingConfig := config.ModelScalingConfigData{
		"default": {KvCacheThreshold: 0.8},
	}
	globalConfig.UpdateModelScalingConfig(modelScalingConfig)
	retrievedConfig := globalConfig.GetModelScalingConfig()
	if retrievedConfig["default"].KvCacheThreshold != 0.8 {
		t.Errorf("Expected threshold 0.8, got %f", retrievedConfig["default"].KvCacheThreshold)
	}

	// Test Concurrency
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			globalConfig.UpdateOptimizationConfig("30s")
			globalConfig.GetOptimizationInterval()
		}()
	}
	wg.Wait()
}

func TestDecisionToOptimizedAlloc(t *testing.T) {
	d := interfaces.VariantDecision{
		TargetReplicas:  3,
		AcceleratorName: "H100",
	}

	replicas, acc, _ := DecisionToOptimizedAlloc(d)

	if replicas != 3 {
		t.Errorf("Expected 3 replicas, got %d", replicas)
	}
	if acc != "H100" {
		t.Errorf("Expected H100 accelerator, got %s", acc)
	}
}
