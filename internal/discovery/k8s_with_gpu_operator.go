package discovery

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// vendors list for GPU vendors
var vendors = []string{
	"nvidia.com",
	"amd.com",
	"intel.com",
}

// K8sWithGpuOperator implements CapacityDiscovery for Kubernetes clusters with GPU Operator
type K8sWithGpuOperator struct {
	Client client.Client
}

// NewK8sWithGpuOperator creates a new K8sWithGpuOperator instance.
func NewK8sWithGpuOperator(client client.Client) *K8sWithGpuOperator {
	return &K8sWithGpuOperator{
		Client: client,
	}
}

// Discover discovers GPU capacity by iterating over nodes and checking GFD labels.
func (d *K8sWithGpuOperator) Discover(ctx context.Context) (map[string]map[string]AcceleratorModelInfo, error) {
	// Optimization: Filter nodes server-side using LabelSelector
	// We only care about nodes that have the NVIDIA GPU product label for now.
	// This prevents listing thousands of CPU-only nodes in large clusters.
	// TODO: To support AMD/Intel in the future, we need to iterate over vendors and perform multiple list calls
	// because K8s LabelSelectors do not support OR logic across different keys (e.g. nvidia OR amd).
	req, err := labels.NewRequirement("nvidia.com/gpu.product", selection.Exists, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create label requirement: %w", err)
	}
	selector := labels.NewSelector().Add(*req)

	// Check for WVA_NODE_SELECTOR environment variable for sharding
	if selectorStr := os.Getenv("WVA_NODE_SELECTOR"); selectorStr != "" {
		userSelector, err := labels.Parse(selectorStr)
		if err != nil {
			return nil, fmt.Errorf("invalid WVA_NODE_SELECTOR: %w", err)
		}
		// Merge user requirements into the existing selector
		requirements, _ := userSelector.Requirements()
		for _, req := range requirements {
			selector = selector.Add(req)
		}
	}

	var nodeList corev1.NodeList
	if err := d.Client.List(ctx, &nodeList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	inv := make(map[string]map[string]AcceleratorModelInfo)

	for _, node := range nodeList.Items {
		nodeName := node.Name

		for _, vendor := range vendors {
			prodKey := vendor + "/gpu.product"
			memKey := vendor + "/gpu.memory"

			if model, ok := node.Labels[prodKey]; ok {
				mem := node.Labels[memKey]
				count := 0
				if cap, ok := node.Status.Allocatable[corev1.ResourceName(vendor+"/gpu")]; ok {
					count = int(cap.Value())
				}

				if inv[nodeName] == nil {
					inv[nodeName] = make(map[string]AcceleratorModelInfo)
				}

				inv[nodeName][model] = AcceleratorModelInfo{
					Count:  count,
					Memory: mem,
				}
			}
		}
	}

	return inv, nil
}
