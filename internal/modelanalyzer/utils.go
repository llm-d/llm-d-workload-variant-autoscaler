package modelanalyzer

import (
	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	inferno "github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/core"
)

// Adapter from inferno allocations to a model analyzer response
func CreateModelAnalyzeResponseFromAllocations(allocations map[string]*inferno.Allocation) *interfaces.ModelAnalyzeResponse {
	responseAllocations := make(map[string]*interfaces.ModelAcceleratorAllocation)

	for key, alloc := range allocations {
		responseAllocations[key] = &interfaces.ModelAcceleratorAllocation{
			Allocation:         allocations[key],
			RequiredPrefillQPS: float64(alloc.MaxArrvRatePerReplica() * 1000),
			RequiredDecodeQPS:  float64(alloc.MaxArrvRatePerReplica() * 1000),
			Reason:             "markovian analysis",
		}
	}
	return &interfaces.ModelAnalyzeResponse{
		Allocations: responseAllocations,
	}
}

// Adapter from a model analyzer response to inferno allocations
func CreateAllocationsFromModelAnalyzeResponse(response *interfaces.ModelAnalyzeResponse) map[string]*inferno.Allocation {
	allocations := make(map[string]*inferno.Allocation)
	for key, alloc := range response.Allocations {
		if alloc.Allocation != nil {
			allocations[key] = alloc.Allocation
		}
	}
	return allocations
}
