package discovery

import "context"

// CapacityDiscovery defines the interface for discovering accelerator capacity in the cluster.
type CapacityDiscovery interface {
	// Discover returns a map of node names to their accelerator inventory.
	// The inner map is keyed by accelerator model name (e.g. "NVIDIA-A100").
	Discover(ctx context.Context) (map[string]map[string]AcceleratorModelInfo, error)
}
