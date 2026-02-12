package core

import (
	"fmt"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/config"
)

// A service class
type ServiceClass struct {
	name     string             // unique name
	priority int                // non-negative priority (smaller values for higher priority)
	targets  map[string]*Target // target SLOs for each model
}

// target SLOs for service class
type Target struct {
	ITL  float32
	TTFT float32
	TPS  float32
}

func (t *Target) String() string {
	return fmt.Sprintf("[ITL=%v, TTFT=%v, TPS=%v]",
		t.ITL, t.TTFT, t.TPS)
}

func NewServiceClass(name string, priority int) *ServiceClass {
	if priority < config.DefaultHighPriority || priority > config.DefaultLowPriority {
		priority = config.DefaultServiceClassPriority
	}
	return &ServiceClass{
		name:     name,
		priority: priority,
		targets:  map[string]*Target{},
	}
}

func NewServiceClassFromSpec(spec *config.ServiceClassSpec) *ServiceClass {
	svc := NewServiceClass(spec.Name, spec.Priority)
	for _, modelTarget := range spec.ModelTargets {
		svc.AddModelTarget(&modelTarget)
	}
	return svc
}

func (c *ServiceClass) Name() string {
	return c.name
}

func (c *ServiceClass) Priority() int {
	return c.priority
}

func (c *ServiceClass) ModelTarget(modelName string) *Target {
	return c.targets[modelName]
}

// update model targets from  service class specification (replace if already exists)
func (c *ServiceClass) UpdateModelTargets(spec *config.ServiceClassSpec) bool {
	if spec.Name != c.name || spec.Priority != c.priority {
		return false
	}
	for _, modelTarget := range spec.ModelTargets {
		c.AddModelTarget(&modelTarget)
	}
	return true
}

// add a model target to the service class (replace if already exists)
func (c *ServiceClass) AddModelTarget(spec *config.ModelTarget) *Target {
	modelName := spec.Model
	target := &Target{
		ITL:  spec.SLO_ITL,
		TTFT: spec.SLO_TTFT,
		TPS:  spec.SLO_TPS,
	}
	c.targets[modelName] = target
	return target
}

func (c *ServiceClass) RemoveModelTarget(modelName string) {
	delete(c.targets, modelName)
}

func (c *ServiceClass) Spec() config.ServiceClassSpec {
	modelTargets := make([]config.ModelTarget, len(c.targets))
	i := 0
	for modelName, target := range c.targets {
		modelTargets[i] = config.ModelTarget{
			Model:    modelName,
			SLO_ITL:  target.ITL,
			SLO_TTFT: target.TTFT,
			SLO_TPS:  target.TPS,
		}
		i++
	}
	return config.ServiceClassSpec{
		Name:         c.name,
		Priority:     c.priority,
		ModelTargets: modelTargets,
	}
}

func (c *ServiceClass) String() string {
	return fmt.Sprintf("ServiceClass: name=%s; priority=%d; targets=%v",
		c.name, c.priority, c.targets)
}
