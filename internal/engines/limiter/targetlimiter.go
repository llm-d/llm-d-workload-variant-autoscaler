package limiter

import (
	"context"
	"fmt"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/solver"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	// DefaultTargetLimiterSaturationPolicy is the default policy used when the limited capacity is saturated
	DefaultTargetLimiterSaturationPolicy = "PriorityRoundRobin"
)

// TargetLimiterConfig holds configuration for the TargetLimiter
type TargetLimiterConfig struct {
	LimiterConfig
	SaturationPolicy string
}

// TargetLimiter distributes the available limited amount of GPUs among variants,
// given their recommended amounts by the engine(s)
type TargetLimiter struct {
	config *TargetLimiterConfig
}

// TODO: Current implementation capitalizes on the optimizer (pkg/solver/optimizer) and its related data.
// Once the Limiter objectives, policies, and algorithms stabilize, consider implementing the limited capacity
// allocation logic directly using workload variants.

// NewTargetLimiter creates a new TargetLimiter instance.
func NewTargetLimiter(config *TargetLimiterConfig) (*TargetLimiter, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if config.SaturationPolicy == "" {
		config.SaturationPolicy = DefaultTargetLimiterSaturationPolicy
	}
	return &TargetLimiter{
		config: config,
	}, nil
}

// Allocate allocates limited GPU capacity among variants by updating their respective decisions
func (l *TargetLimiter) Allocate(
	ctx context.Context,
	decisions []interfaces.VariantDecision,
	inventory map[string]map[string]collector.AcceleratorModelInfo,
) error {
	logger := ctrl.LoggerFrom(ctx)

	// Prepare system data for the optimizer
	system, err := prepareSystemData(inventory, decisions)
	if err != nil {
		return err
	}

	// create solver in limited mode
	optimizerSpec := &config.OptimizerSpec{
		Unlimited:        false,
		SaturationPolicy: DefaultTargetLimiterSaturationPolicy,
	}
	optimizer := solver.NewOptimizerFromSpec(optimizerSpec)
	core.TheSystem = system

	// run optimization
	err = optimizer.Optimize()
	system.AllocateByType()
	if err != nil {
		return err
	}

	// Update decisions based on optimized allocations
	decisionMap := make(map[string]*interfaces.VariantDecision)
	for i, d := range decisions {
		decisionMap[d.VariantName] = &decisions[i]
	}
	for name, server := range system.Servers() {
		var targetReplicas int
		var cost float64
		if alloc := server.Allocation(); alloc != nil {
			targetReplicas = alloc.NumReplicas()
			cost = float64(alloc.Cost())
		}
		decision, exists := decisionMap[name]
		if !exists {
			logger.Info("No decision found for variant", "variant", name)
			continue
		}
		logger.Info("Limited allocation for variant", "variant", name, "targetReplicas", targetReplicas, "cost", cost)
		// TODO: For now, we are overwriting the TargetReplicas and Cost based on the optimizer's output.
		(*decision).TargetReplicas = targetReplicas
		(*decision).Cost = cost
	}

	logger.Info("Limited capacity allocation completed successfully")

	return nil
}

// prepareSystemData prepares the system data required by the optimizer
func prepareSystemData(
	inventory map[string]map[string]collector.AcceleratorModelInfo,
	decisions []interfaces.VariantDecision,
) (*core.System, error) {

	// create capacity data
	capData := createCapacityData(inventory)

	// create accelerator data from capacity data
	accData := createAcceleratorData(capData)

	// create model data
	modelData, modelNames := createModelData(decisions)

	// create service classes
	svcClassData := createServiceClassData(modelNames)

	// create server data
	serverData := createServerData(decisions)

	// create system and set all spec data
	system := core.NewSystem()
	system.SetAcceleratorsFromSpec(accData)
	system.SetModelsFromSpec(modelData)
	system.SetServiceClassesFromSpec(svcClassData)
	system.SetServersFromSpec(serverData)
	system.SetCapacityFromSpec(capData)

	// set allocations for servers
	setAllocationsForServers(system, decisions)

	return system, nil
}

// createCapacityData creates CapacityData from the discovered inventory
func createCapacityData(
	inventory map[string]map[string]collector.AcceleratorModelInfo,
) *config.CapacityData {
	capData := &config.CapacityData{
		Count: []config.AcceleratorCount{},
	}
	// Sum up the counts of each accelerator type across all nodes
	accCountMap := make(map[string]int)
	for _, accMap := range inventory {
		for accType, accInfo := range accMap {
			if accInfo.Count <= 0 {
				continue
			}
			curCount := accCountMap[accType]
			accCountMap[accType] = curCount + accInfo.Count
		}
	}
	capData.Count = make([]config.AcceleratorCount, len(accCountMap))
	i := 0
	for accType, count := range accCountMap {
		capData.Count[i] = config.AcceleratorCount{
			Type:  accType,
			Count: count,
		}
		i++
	}
	return capData
}

// createAcceleratorData creates AcceleratorData from the CapacityData
// assumes that the Count in CapacityData is an array of unique accelerators,
// each having a unique nonempty Type and a positive Count.
func createAcceleratorData(
	capData *config.CapacityData,
) *config.AcceleratorData {
	accData := &config.AcceleratorData{
		Spec: []config.AcceleratorSpec{},
	}
	accData.Spec = make([]config.AcceleratorSpec, len(capData.Count))
	for i, accCount := range capData.Count {
		accData.Spec[i] = config.AcceleratorSpec{
			Name: accCount.Type,
			Type: accCount.Type,
			Cost: 0, // TODO: Update this to reflect actual costs if available.
			// For now, it can be set to 0 or a default value since the optimizer
			// will use the cost values from the server allocations rather than
			// the accelerator specs directly in this limited capacity scenario.
			Multiplicity: 1, // TODO: This is a placeholder.
			// The actual multiplicity should be determined based on how many of this accelerator type can be allocated together.
			// For example, if the system can allocate 4 GPUs of this type together, the multiplicity would be 4.
			// This information might come from the inventory or be defined as part of the system's configuration.
			// For now, it's set to 1 as a default value.
		}
	}
	return accData
}

// createModelData creates ModelData from the decisions
func createModelData(
	decisions []interfaces.VariantDecision,
) (modelData *config.ModelData, modelNames []string) {
	modelData = &config.ModelData{
		PerfData: []config.ModelAcceleratorPerfData{},
	}
	modelNames = []string{}
	modelAcceleratorMap := make(map[string]map[string]int)
	for _, decision := range decisions {
		model := decision.ModelID
		acc := decision.AcceleratorName
		if modelAcceleratorMap[model] == nil {
			modelAcceleratorMap[model] = make(map[string]int)
			modelNames = append(modelNames, model)
		}
		modelAcceleratorMap[model][acc] = decision.GPUsPerReplica
	}
	for model, accMap := range modelAcceleratorMap {
		for acc, count := range accMap {
			modelData.PerfData = append(modelData.PerfData, config.ModelAcceleratorPerfData{
				Name:     model,
				Acc:      acc,
				AccCount: count,
			})
		}
	}
	return modelData, modelNames
}

// createServiceClassData creates ServiceClassData from the model names
func createServiceClassData(
	modelNames []string,
) *config.ServiceClassData {
	svcClassData := &config.ServiceClassData{
		Spec: []config.ServiceClassSpec{
			{
				Name: config.DefaultServiceClassName,
				// TODO: The current implementation assumes that all models belong to the same service class.
				// In a more complex scenario, you might have different service classes for different models or groups of models.
				// The ModelTargets field is populated with all the model names to indicate that this service class can serve all the models.
				// In a real implementation, you would want to determine which models belong to which service classes based on your specific
				// requirements and configurations. For now, we are assigning all models to the default service class for simplicity.
				Priority: 1,
				ModelTargets: func() []config.ModelTarget {
					targets := make([]config.ModelTarget, len(modelNames))
					for i, modelName := range modelNames {
						targets[i] = config.ModelTarget{
							Model: modelName,
						}
					}
					return targets
				}(),
			},
		},
	}
	return svcClassData
}

// createServerData creates ServerData from the decisions
func createServerData(decisions []interfaces.VariantDecision) *config.ServerData {
	serverData := &config.ServerData{
		Spec: []config.ServerSpec{},
	}
	serverData.Spec = make([]config.ServerSpec, len(decisions))
	for i, decision := range decisions {
		serverData.Spec[i] = config.ServerSpec{
			Name:            decision.VariantName,
			Model:           decision.ModelID,
			Class:           config.DefaultServiceClassName,
			KeepAccelerator: true,
			CurrentAlloc: config.AllocationData{
				Accelerator: decision.AcceleratorName,
				NumReplicas: decision.CurrentReplicas,
			},
		}
	}
	return serverData
}

// setAllocationsForServers sets the allocations for servers based on the decisions
func setAllocationsForServers(
	system *core.System,
	decisions []interfaces.VariantDecision,
) {
	for _, decision := range decisions {
		name := decision.VariantName
		server := system.Server(name)
		if server == nil {
			continue
		}
		alloc := &config.AllocationData{
			Accelerator: decision.AcceleratorName,
			NumReplicas: decision.TargetReplicas,
			Cost:        float32(decision.Cost),
		}
		server.SetAllAllocations([]*config.AllocationData{alloc})
		// set allocation values as accelerator costs
		for _, a := range server.AllAllocations() {
			a.SetValue(a.Cost())
		}
	}
}
