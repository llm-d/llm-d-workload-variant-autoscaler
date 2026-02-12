package solver

import (
	"testing"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/core"
)

func TestNewOptimizer(t *testing.T) {
	tests := []struct {
		name          string
		optimizerSpec *config.OptimizerSpec
		Optimizer     *Optimizer
		Solver        *Solver
		setup         func(optimizerSpec *config.OptimizerSpec)
		wantErr       bool
	}{
		{
			name: "valid optimizer spec",
			optimizerSpec: &config.OptimizerSpec{
				Unlimited:        false,
				SaturationPolicy: "None",
			},
			Optimizer: NewOptimizerFromSpec(&config.OptimizerSpec{
				Unlimited:        false,
				SaturationPolicy: "None",
			}),
			Solver: NewSolver(&config.OptimizerSpec{Unlimited: false, SaturationPolicy: "None"}),
			setup: func(optimizerSpec *config.OptimizerSpec) {
				system := core.NewSystem()
				system.SetFromSpec(&config.SystemSpec{
					Accelerators: config.AcceleratorData{
						Spec: []config.AcceleratorSpec{
							{
								Name: "A100",
								Power: config.PowerSpec{
									Idle:     50,
									MidPower: 150,
									Full:     350,
									MidUtil:  0.4,
								},
							},
						},
					},
					Models: config.ModelData{
						PerfData: []config.ModelAcceleratorPerfData{
							{
								Name:     "llama-7b",
								Acc:      "A100",
								AccCount: 1,
							},
						},
					},
					Capacity: config.CapacityData{
						Count: []config.AcceleratorCount{
							{
								Type:  "A100",
								Count: 2,
							},
						},
					},
					Servers: config.ServerData{
						Spec: []config.ServerSpec{
							{
								Name:            "server1",
								Class:           "default",
								Model:           "llama-7b",
								KeepAccelerator: true,
								MinNumReplicas:  1,
								MaxBatchSize:    512,
								CurrentAlloc: config.AllocationData{
									Accelerator: "A100",
									NumReplicas: 1,
								},
							},
						},
					},
					ServiceClasses: config.ServiceClassData{
						Spec: []config.ServiceClassSpec{
							{
								Name:     "default",
								Priority: 1,
								ModelTargets: []config.ModelTarget{
									{
										Model:    "llama-7b",
										SLO_ITL:  9,
										SLO_TTFT: 1000,
									},
								},
							},
						},
					},
					Optimizer: config.OptimizerData{
						Spec: *optimizerSpec,
					},
				})
				core.TheSystem = system
			},
			wantErr: false,
		},
		{
			name: "unlimited optimizer spec",
			optimizerSpec: &config.OptimizerSpec{
				Unlimited:        true,
				SaturationPolicy: "None",
			},
			Optimizer: NewOptimizerFromSpec(&config.OptimizerSpec{
				Unlimited:        true,
				SaturationPolicy: "None",
			}),
			Solver: NewSolver(&config.OptimizerSpec{Unlimited: true, SaturationPolicy: "None"}),
			setup: func(optimizerSpec *config.OptimizerSpec) {
				system := core.NewSystem()
				system.SetFromSpec(&config.SystemSpec{
					Accelerators: config.AcceleratorData{
						Spec: []config.AcceleratorSpec{
							{
								Name: "A100",
								Power: config.PowerSpec{
									Idle:     50,
									MidPower: 150,
									Full:     350,
									MidUtil:  0.4,
								},
							},
						},
					},
					Models: config.ModelData{
						PerfData: []config.ModelAcceleratorPerfData{
							{
								Name:     "llama-7b",
								Acc:      "A100",
								AccCount: 1,
							},
						},
					},
					Capacity: config.CapacityData{
						Count: []config.AcceleratorCount{
							{
								Type:  "A100",
								Count: 2,
							},
						},
					},
					Servers: config.ServerData{
						Spec: []config.ServerSpec{
							{
								Name:            "server1",
								Class:           "default",
								Model:           "llama-7b",
								KeepAccelerator: true,
								MinNumReplicas:  1,
								MaxBatchSize:    512,
								CurrentAlloc: config.AllocationData{
									Accelerator: "A100",
									NumReplicas: 1,
								},
							},
						},
					},
					ServiceClasses: config.ServiceClassData{
						Spec: []config.ServiceClassSpec{
							{
								Name:     "default",
								Priority: 1,
								ModelTargets: []config.ModelTarget{
									{
										Model:    "llama-7b",
										SLO_ITL:  9,
										SLO_TTFT: 1000,
									},
								},
							},
						},
					},
					Optimizer: config.OptimizerData{
						Spec: *optimizerSpec,
					},
				})
				core.TheSystem = system
			},
			wantErr: false,
		},
		{
			name:          "nil optimizer spec",
			optimizerSpec: nil,
			Optimizer:     NewOptimizerFromSpec(nil),
			Solver:        nil,
			wantErr:       true, // Optimize() should fail
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(tt.optimizerSpec)
			}

			optimizer := tt.Optimizer
			err := optimizer.Optimize()

			if err == nil && tt.wantErr {
				t.Fatal("NewOptimizer() should have failed but didn't")
			}
			if err != nil && !tt.wantErr {
				t.Fatal("NewOptimizer() should not have failed but did fail")
			}
			if optimizer != nil {
				if optimizer.spec == nil && err == nil {
					t.Error("spec map not initialized")
				}
				if optimizer.SolutionTimeMsec() < 0 {
					t.Error("solutionTimeMsec should be non-negative")
				}
			}
		})
	}
}

func TestOptimizer_String(t *testing.T) {
	optimizerSpec := &config.OptimizerSpec{
		Unlimited:        false,
		SaturationPolicy: "None",
	}

	solver := NewSolver(optimizerSpec)
	optimizer := &Optimizer{
		spec:   optimizerSpec,
		solver: solver,
	}

	// String method should not panic and return something
	str := optimizer.String()
	if str == "" {
		t.Error("Optimizer.String() returned empty string")
	}
}
