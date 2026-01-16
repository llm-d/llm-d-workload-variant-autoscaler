package limiter

import (
	"context"
	"reflect"
	"testing"

	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/collector"
	"github.com/llm-d-incubation/workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/config"
	"github.com/llm-d-incubation/workload-variant-autoscaler/pkg/core"
)

func TestNewTargetLimiter(t *testing.T) {
	tests := []struct {
		name    string
		config  *TargetLimiterConfig
		want    *TargetLimiter
		wantErr bool
	}{
		{
			name: "Test case 1: Valid configuration",
			config: &TargetLimiterConfig{
				LimiterConfig:    LimiterConfig{},
				SaturationPolicy: "PriorityRoundRobin",
				ServiceClassName: "Default",
			},
			want: &TargetLimiter{
				&TargetLimiterConfig{
					SaturationPolicy: "PriorityRoundRobin",
					ServiceClassName: "Default",
				},
			},
			wantErr: false,
		},
		{
			name:    "Test case 2: Nil configuration",
			config:  nil,
			want:    nil,
			wantErr: true,
		},
		{
			name: "Test case 3: Empty saturation policy",
			config: &TargetLimiterConfig{
				SaturationPolicy: "",
				ServiceClassName: "Default",
			},
			want: &TargetLimiter{
				&TargetLimiterConfig{
					LimiterConfig:    LimiterConfig{},
					SaturationPolicy: "PriorityRoundRobin",
					ServiceClassName: "Default",
				},
			},
			wantErr: false,
		},
		{
			name: "Test case 4: Empty service class name",
			config: &TargetLimiterConfig{
				LimiterConfig:    LimiterConfig{},
				SaturationPolicy: "PriorityRoundRobin",
				ServiceClassName: "",
			},
			want: &TargetLimiter{
				&TargetLimiterConfig{
					SaturationPolicy: "PriorityRoundRobin",
					ServiceClassName: "Default",
				},
			},
			wantErr: false,
		},
		{
			name: "Test case 5: Other saturation policy",
			config: &TargetLimiterConfig{
				LimiterConfig:    LimiterConfig{},
				SaturationPolicy: "Random",
				ServiceClassName: "Free",
			},
			want: &TargetLimiter{
				&TargetLimiterConfig{
					LimiterConfig:    LimiterConfig{},
					SaturationPolicy: "Random",
					ServiceClassName: "Free",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := NewTargetLimiter(tt.config)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("NewTargetLimiter() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("NewTargetLimiter() succeeded unexpectedly")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewTargetLimiter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTargetLimiter_Allocate(t *testing.T) {
	tests := []struct {
		name      string
		config    *TargetLimiterConfig
		decisions []interfaces.VariantDecision
		inventory map[string]map[string]collector.AcceleratorModelInfo
		wantErr   bool
	}{
		{
			name:   "Test case 1: Basic input",
			config: &TargetLimiterConfig{},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 2,
					TargetReplicas:  6,
					Cost:            90,
					GPUsPerReplica:  1,
				},
			},
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: 4},
				},
			},
			wantErr: false,
		},
		{
			name:   "Test case 2: Multiple variants",
			config: &TargetLimiterConfig{},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 2,
					TargetReplicas:  6,
					Cost:            90,
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 1,
					TargetReplicas:  4,
					Cost:            60,
					GPUsPerReplica:  1,
				},
			},
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: 7},
				},
			},
			wantErr: false,
		},
		{
			name:   "Test case 3: Zero inventory",
			config: &TargetLimiterConfig{},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 2,
					TargetReplicas:  6,
					Cost:            90,
					GPUsPerReplica:  1,
				},
			},
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"amd": {Count: 3},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := NewTargetLimiter(tt.config)
			if err != nil {
				t.Fatalf("could not construct receiver type: %v", err)
			}
			gotErr := l.Allocate(context.Background(), tt.decisions, tt.inventory)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Allocate() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Allocate() succeeded unexpectedly")
			}
			allocated := GetAllocatedMap(tt.decisions)
			available := GetAvailableMap(tt.inventory)
			for acc, count := range allocated {
				if count > available[acc] {
					t.Errorf("Allocate() allocated %d of %s, but only %d available", count, acc, available[acc])
				}
			}
		})
	}
}

func Test_createCapacityData(t *testing.T) {
	tests := []struct {
		name      string
		inventory map[string]map[string]collector.AcceleratorModelInfo
		want      *config.CapacityData
	}{
		{
			name: "Test case 1: Basic input",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: 2},
				},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 2},
				},
			},
		},
		{
			name: "Test case 2: Multiple nodes and different accelerators",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: 2},
				},
				"node-2": {
					"nvidia-tesla-v100": {Count: 4},
				},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 2},
					{Type: "nvidia-tesla-v100", Count: 4},
				},
			},
		},
		{
			name: "Test case 3: Multiple nodes and same accelerators",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: 2},
				},
				"node-2": {
					"nvidia-tesla-k80": {Count: 4},
				},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 6},
				},
			},
		},
		{
			name: "Test case 4: No accelerators",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {},
				"node-2": {},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{},
			},
		},
		{
			name: "Test case 5: Negative counts (should be ignored)",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80": {Count: -2},
				},
				"node-2": {
					"nvidia-tesla-v100": {Count: -4},
				},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{},
			},
		},
		{
			name: "Test case 6: Mixed valid and invalid counts",
			inventory: map[string]map[string]collector.AcceleratorModelInfo{
				"node-1": {
					"nvidia-tesla-k80":  {Count: 2},
					"nvidia-tesla-v100": {Count: -4},
				},
				"node-2": {
					"nvidia-tesla-k80":  {Count: -2},
					"nvidia-tesla-v100": {Count: 4},
				},
			},
			want: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 2},
					{Type: "nvidia-tesla-v100", Count: 4},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createCapacityData(tt.inventory)
			if !EqualSlicesUnorderedComparable(got.Count, tt.want.Count) {
				t.Errorf("createCapacityData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createAcceleratorData(t *testing.T) {
	tests := []struct {
		name    string
		capData *config.CapacityData
		want    *config.AcceleratorData
	}{
		{
			name: "Test case 1: Basic input",
			capData: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 2},
				},
			},
			want: &config.AcceleratorData{
				Spec: []config.AcceleratorSpec{
					{
						Name:         "nvidia-tesla-k80",
						Type:         "nvidia-tesla-k80",
						Multiplicity: 1,
					},
				},
			},
		},
		{
			name: "Test case 2: Multiple accelerators",
			capData: &config.CapacityData{
				Count: []config.AcceleratorCount{
					{Type: "nvidia-tesla-k80", Count: 2},
					{Type: "nvidia-tesla-v100", Count: 4},
				},
			},
			want: &config.AcceleratorData{
				Spec: []config.AcceleratorSpec{
					{
						Name:         "nvidia-tesla-k80",
						Type:         "nvidia-tesla-k80",
						Multiplicity: 1,
					},
					{
						Name:         "nvidia-tesla-v100",
						Type:         "nvidia-tesla-v100",
						Multiplicity: 1,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createAcceleratorData(tt.capData)
			if !EqualSlicesUnorderedComparable(got.Spec, tt.want.Spec) {
				t.Errorf("createAcceleratorData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createModelData(t *testing.T) {
	tests := []struct {
		name      string
		decisions []interfaces.VariantDecision
		want      *config.ModelData
		want2     []string
	}{
		{
			name: "Test case 1: Basic input",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					GPUsPerReplica:  1,
				},
			},
			want: &config.ModelData{
				PerfData: []config.ModelAcceleratorPerfData{
					{
						Name:     "model-1",
						Acc:      "nvidia-tesla-k80",
						AccCount: 1,
					},
				},
			},
			want2: []string{"model-1"},
		},
		{
			name: "Test case 2: Multiple decisions with different models and accelerators",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-v100",
					GPUsPerReplica:  2,
				},
			},
			want: &config.ModelData{
				PerfData: []config.ModelAcceleratorPerfData{
					{
						Name:     "model-1",
						Acc:      "nvidia-tesla-k80",
						AccCount: 1,
					},
					{
						Name:     "model-2",
						Acc:      "nvidia-tesla-v100",
						AccCount: 2,
					},
				},
			},
			want2: []string{"model-1", "model-2"},
		},
		{
			name: "Test case 3: Multiple decisions with the same model and different accelerators",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-v100",
					GPUsPerReplica:  1,
				},
			},
			want: &config.ModelData{
				PerfData: []config.ModelAcceleratorPerfData{
					{
						Name:     "model-1",
						Acc:      "nvidia-tesla-k80",
						AccCount: 1,
					},
					{
						Name:     "model-1",
						Acc:      "nvidia-tesla-v100",
						AccCount: 1,
					},
				},
			},
			want2: []string{"model-1"},
		},
		{
			name: "Test case 4: Multiple decisions with the different models and same accelerator",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-k80",
					GPUsPerReplica:  1,
				},
			},
			want: &config.ModelData{
				PerfData: []config.ModelAcceleratorPerfData{
					{
						Name:     "model-1",
						Acc:      "nvidia-tesla-k80",
						AccCount: 1,
					},
					{
						Name:     "model-2",
						Acc:      "nvidia-tesla-k80",
						AccCount: 1,
					},
				},
			},
			want2: []string{"model-1", "model-2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got2 := createModelData(tt.decisions)
			if !EqualSlicesUnorderedComparable(got.PerfData, tt.want.PerfData) {
				t.Errorf("createModelData() modelData = %v, want %v", got, tt.want)
			}
			if !EqualSlicesUnorderedComparable(got2, tt.want2) {
				t.Errorf("createModelData() modelNames= %v, want %v", got2, tt.want2)
			}
		})
	}
}

func Test_createServiceClassData(t *testing.T) {
	tests := []struct {
		name       string
		modelNames []string
		want       *config.ServiceClassData
	}{
		{
			name:       "Test case 1: Basic input",
			modelNames: []string{"model-1"},
			want: &config.ServiceClassData{
				Spec: []config.ServiceClassSpec{
					{
						Name:     DefaultTargetLimiterServiceClassName,
						Priority: 1,
						ModelTargets: []config.ModelTarget{
							{Model: "model-1"},
						},
					},
				},
			},
		},
		{
			name:       "Test case 2: Multiple models",
			modelNames: []string{"model-1", "model-2"},
			want: &config.ServiceClassData{
				Spec: []config.ServiceClassSpec{
					{
						Name:     DefaultTargetLimiterServiceClassName,
						Priority: 1,
						ModelTargets: []config.ModelTarget{
							{Model: "model-1"},
							{Model: "model-2"},
						},
					},
				},
			},
		},
		{
			name:       "Test case 3: No models",
			modelNames: []string{},
			want: &config.ServiceClassData{
				Spec: []config.ServiceClassSpec{
					{
						Name:         DefaultTargetLimiterServiceClassName,
						Priority:     1,
						ModelTargets: []config.ModelTarget{},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createServiceClassData(tt.modelNames)
			if !EqualSlicesUnorderedAny(got.Spec, tt.want.Spec) {
				t.Errorf("createServiceClassData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_createServerData(t *testing.T) {
	tests := []struct {
		name      string
		decisions []interfaces.VariantDecision
		want      *config.ServerData
	}{
		{
			name: "Test case 1: Basic input",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 2,
					GPUsPerReplica:  1,
				},
			},
			want: &config.ServerData{
				Spec: []config.ServerSpec{
					{
						Name:            "variant-1",
						Model:           "model-1",
						Class:           DefaultTargetLimiterServiceClassName,
						KeepAccelerator: true,
						CurrentAlloc: config.AllocationData{
							Accelerator: "nvidia-tesla-k80",
							NumReplicas: 2,
						},
					},
				},
			},
		},
		{
			name: "Test case 2: Multiple decisions",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 2,
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-v100",
					CurrentReplicas: 4,
					GPUsPerReplica:  1,
				},
			},
			want: &config.ServerData{
				Spec: []config.ServerSpec{
					{
						Name:            "variant-1",
						Model:           "model-1",
						Class:           DefaultTargetLimiterServiceClassName,
						KeepAccelerator: true,
						CurrentAlloc: config.AllocationData{
							Accelerator: "nvidia-tesla-k80",
							NumReplicas: 2,
						},
					},
					{
						Name:            "variant-2",
						Model:           "model-2",
						Class:           DefaultTargetLimiterServiceClassName,
						KeepAccelerator: true,
						CurrentAlloc: config.AllocationData{
							Accelerator: "nvidia-tesla-v100",
							NumReplicas: 4,
						},
					},
				},
			},
		},
		{
			name:      "Test case 3: No decisions",
			decisions: []interfaces.VariantDecision{},
			want: &config.ServerData{
				Spec: []config.ServerSpec{},
			},
		},
		{
			name: "Test case 4: Decisions with zero replicas",
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 0,
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-k80",
					CurrentReplicas: 10,
					GPUsPerReplica:  1,
				},
			},
			want: &config.ServerData{
				Spec: []config.ServerSpec{
					{
						Name:            "variant-1",
						Model:           "model-1",
						Class:           DefaultTargetLimiterServiceClassName,
						KeepAccelerator: true,
						CurrentAlloc: config.AllocationData{
							Accelerator: "nvidia-tesla-k80",
							NumReplicas: 0,
						},
					},
					{
						Name:            "variant-2",
						Model:           "model-2",
						Class:           DefaultTargetLimiterServiceClassName,
						KeepAccelerator: true,
						CurrentAlloc: config.AllocationData{
							Accelerator: "nvidia-tesla-k80",
							NumReplicas: 10,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createServerData(tt.decisions)
			if !EqualSlicesUnorderedAny(got.Spec, tt.want.Spec) {
				t.Errorf("createServerData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_setAllocationsForServers(t *testing.T) {
	tests := []struct {
		name        string
		serverSpecs []config.ServerSpec
		decisions   []interfaces.VariantDecision
	}{
		{
			name: "Test case 1: Basic input",
			serverSpecs: []config.ServerSpec{
				{
					Name:  "variant-1",
					Model: "model-1",
				},
			},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					TargetReplicas:  2,
					Cost:            100,
					GPUsPerReplica:  1,
				},
			},
		},
		{
			name: "Test case 2: Multiple servers and decisions",
			serverSpecs: []config.ServerSpec{
				{
					Name:  "variant-1",
					Model: "model-1",
				},
				{
					Name:  "variant-2",
					Model: "model-2",
				},
			},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					TargetReplicas:  2,
					Cost:            100,
					GPUsPerReplica:  1,
				},
				{
					VariantName:     "variant-2",
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-v100",
					TargetReplicas:  4,
					Cost:            200,
					GPUsPerReplica:  1,
				},
			},
		},
		{
			name: "Test case 3: Decision with no matching server",
			serverSpecs: []config.ServerSpec{
				{
					Name:  "variant-1",
					Model: "model-1",
				},
			},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-2", // No matching server
					ModelID:         "model-2",
					AcceleratorName: "nvidia-tesla-v100",
					TargetReplicas:  4,
					Cost:            200,
					GPUsPerReplica:  1,
				},
			},
		},
		{
			name: "Test case 4: Decision with zero target replicas",
			serverSpecs: []config.ServerSpec{
				{
					Name:  "variant-1",
					Model: "model-1",
				},
			},
			decisions: []interfaces.VariantDecision{
				{
					VariantName:     "variant-1",
					ModelID:         "model-1",
					AcceleratorName: "nvidia-tesla-k80",
					TargetReplicas:  0,
					Cost:            0,
					GPUsPerReplica:  1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system := core.NewSystem()
			system.SetServersFromSpec(&config.ServerData{Spec: tt.serverSpecs})
			setAllocationsForServers(system, tt.decisions)
			for _, d := range tt.decisions {
				server := system.Server(d.VariantName)
				if server == nil {
					continue
				}
				allAllocations := server.AllAllocations()
				if len(allAllocations) != 1 {
					t.Errorf("Expected 1 allocation for server %s, got %d", d.VariantName, len(allAllocations))
					continue
				}
				if alloc, exists := allAllocations[d.AcceleratorName]; exists {
					if alloc.Accelerator() != d.AcceleratorName {
						t.Errorf("Expected accelerator %s for server %s, got %s", d.AcceleratorName, d.VariantName, alloc.Accelerator())
					}
					if alloc.NumReplicas() != d.TargetReplicas {
						t.Errorf("Expected %d replicas for server %s, got %d", d.TargetReplicas, d.VariantName, alloc.NumReplicas())
					}
					if alloc.Cost() != float32(d.Cost) {
						t.Errorf("Expected cost %v for server %s, got %v", d.Cost, d.VariantName, alloc.Cost())
					}
				} else {
					t.Errorf("Allocation for server %s and accelerator %s not found", d.VariantName, d.AcceleratorName)
				}
			}
		})
	}
}

// EqualSlicesUnorderedComparable checks if two slices contain the same elements with the same frequencies.
func EqualSlicesUnorderedComparable[T comparable](s1, s2 []T) bool {
	// 1. Quick check: if lengths differ, they can't be equal.
	if len(s1) != len(s2) {
		return false
	}

	// 2. Count frequencies in the first slice.
	counts1 := make(map[T]int)
	for _, item := range s1 {
		counts1[item]++
	}

	// 3. Count frequencies in the second slice.
	counts2 := make(map[T]int)
	for _, item := range s2 {
		counts2[item]++
	}

	// 4. Compare the frequency maps.
	for key, count1 := range counts1 {
		if count2, exists := counts2[key]; !exists || count1 != count2 {
			return false // Element missing or count mismatch
		}
	}

	// Since lengths were equal and all counts matched, they are equal.
	return true
}

// EqualSlicesUnorderedAny checks if two slices contain the same elements irrespective of order.
func EqualSlicesUnorderedAny[T any](s1, s2 []T) bool {
	// 1. Quick check: if lengths differ, they can't be equal.
	if len(s1) != len(s2) {
		return false
	}

	// 2. Check if every element in s1 is present in s2.
	for _, a := range s1 {
		found := false
		for _, b := range s2 {
			if reflect.DeepEqual(a, b) {
				found = true
				break
			}
		}
		if !found {
			return false // Element from s1 not found in s2
		}
	}

	// Since lengths were equal and all elements are found.
	return true
}

// GetAllocatedMap creates a map of accelerator name to total allocated replicas from the decisions.
func GetAllocatedMap(decisions []interfaces.VariantDecision) map[string]int {
	allocated := make(map[string]int)
	for _, d := range decisions {
		existing := allocated[d.AcceleratorName]
		allocated[d.AcceleratorName] = existing + d.TargetReplicas
	}
	return allocated
}

// GetAvailableMap creates a map of accelerator name to total available count from the inventory.
func GetAvailableMap(inventory map[string]map[string]collector.AcceleratorModelInfo) map[string]int {
	available := make(map[string]int)
	for _, accMap := range inventory {
		for acc, info := range accMap {
			existing := available[acc]
			available[acc] = existing + info.Count
		}
	}
	return available
}
