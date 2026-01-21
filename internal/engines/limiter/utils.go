package limiter

import (
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
)

// GetAvailableInventory calculates the available inventory of accelerators that are currently
// not allocated to any variants based on the current decisions and the total inventory.
func GetAvailableInventory(
	inventory map[string]map[string]collector.AcceleratorModelInfo,
	decisions *[]interfaces.VariantDecision,
) map[string]collector.AcceleratorModelInfo {
	// Sum up the counts of each accelerator type across all nodes to get the total inventory.
	availableInventory := make(map[string]collector.AcceleratorModelInfo)
	for _, accMap := range inventory {
		for accType, accInfo := range accMap {
			if accInfo.Count <= 0 {
				continue
			}
			curInfo, exists := availableInventory[accType]
			if exists {
				curInfo.Count += accInfo.Count
			}
			availableInventory[accType] = curInfo
		}
	}

	// Subtract the counts of each accelerator type that have been allocated to variants based on the decisions.
	for _, d := range *decisions {
		accType := d.AcceleratorName
		if accInfo, exists := availableInventory[accType]; exists {
			accInfo.Count -= d.CurrentReplicas * d.GPUsPerReplica
			if accInfo.Count < 0 {
				accInfo.Count = 0
			}
			availableInventory[accType] = accInfo
		}
	}
	return availableInventory
}
