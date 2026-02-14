package manager

import (
	"testing"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/core"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/solver"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name      string
		system    *core.System
		optimizer *solver.Optimizer
		wantNil   bool
	}{
		{
			name:      "create manager with valid system and optimizer",
			system:    &core.System{},
			optimizer: &solver.Optimizer{},
			wantNil:   false,
		},
		{
			name:      "create manager with nil system",
			system:    nil,
			optimizer: &solver.Optimizer{},
			wantNil:   false,
		},
		{
			name:      "create manager with nil optimizer",
			system:    &core.System{},
			optimizer: nil,
			wantNil:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewManager(tt.system, tt.optimizer)
			if (got == nil) != tt.wantNil {
				t.Errorf("NewManager() = %v, wantNil %v", got, tt.wantNil)
			}

			if got != nil {
				if got.system != tt.system {
					t.Errorf("NewManager().system = %v, want %v", got.system, tt.system)
				}
				if got.optimizer != tt.optimizer {
					t.Errorf("NewManager().optimizer = %v, want %v", got.optimizer, tt.optimizer)
				}
				// Verify that core.TheSystem is set
				if core.TheSystem != tt.system {
					t.Errorf("core.TheSystem = %v, want %v", core.TheSystem, tt.system)
				}
			}
		})
	}
}

func TestManager_Optimize(t *testing.T) {
	createTestSystem := func() *core.System {
		system := core.NewSystem()

		// Add accelerator
		accSpec := config.AcceleratorSpec{
			Name:         "test-gpu",
			Type:         "gpu",
			Cost:         100.0,
			Multiplicity: 1,
		}
		system.AddAcceleratorFromSpec(accSpec)

		// Set capacity
		capacityCount := config.AcceleratorCount{
			Type:  "gpu",
			Count: 10,
		}
		system.SetCountFromSpec(capacityCount)

		// Add model
		model := system.AddModel("test-model")

		// Add performance data
		perfData := &config.ModelAcceleratorPerfData{
			Name:         "test-model",
			Acc:          "test-gpu",
			AccCount:     1,
			MaxBatchSize: 16,
			AtTokens:     200,
			ServiceParms: config.ServiceParms{
				Alpha: 5.0,
				Beta:  0.2,
				Gamma: 0.015,
			},
		}
		model.AddPerfDataFromSpec(perfData)

		// Add service class with target
		system.AddServiceClass("default", 5)
		serviceClass := system.ServiceClass("default")
		if serviceClass != nil {
			modelTarget := &config.ModelTarget{
				Model:    "test-model",
				SLO_TTFT: 2000.0, // 2 seconds
				SLO_ITL:  500.0,  // 500ms
				SLO_TPS:  0.0,
			}
			serviceClass.AddModelTarget(modelTarget)
		}

		// Add server with non-zero load
		serverSpec := config.ServerSpec{
			Name:           "test-server",
			Model:          "test-model",
			Class:          "default",
			MinNumReplicas: 1,
			CurrentAlloc: config.AllocationData{
				Load: config.ServerLoadSpec{
					ArrivalRate:  60.0, // Non-zero arrival rate (1 req/sec)
					AvgInTokens:  100,
					AvgOutTokens: 200,
				},
			},
		}
		system.AddServerFromSpec(serverSpec)

		return system
	}

	tests := []struct {
		name         string
		setupManager func() *Manager
		wantErr      bool
		checkResult  func(t *testing.T, manager *Manager)
	}{
		{
			name: "successful optimization with realistic system",
			setupManager: func() *Manager {
				system := createTestSystem()
				optimizerSpec := &config.OptimizerSpec{
					Unlimited:        false,
					SaturationPolicy: "None",
				}
				optimizer := solver.NewOptimizerFromSpec(optimizerSpec)
				manager := NewManager(system, optimizer)
				system.Calculate()

				return manager
			},
			wantErr: false,
			checkResult: func(t *testing.T, manager *Manager) {
				// Verify that solution time is >= 0
				solutionTime := manager.optimizer.SolutionTimeMsec()
				if solutionTime < 0 {
					t.Error("Optimize() should have set solution time >= 0")
				}
				t.Logf("Optimization completed in %d msec", solutionTime)

				// Verify that servers have allocations after optimization
				servers := manager.system.Servers()
				if len(servers) == 0 {
					t.Fatal("No servers in system - test setup is invalid")
				}

				allocatedCount := 0
				for _, server := range servers {
					if server.Allocation() != nil {
						allocatedCount++
						alloc := server.Allocation()
						t.Logf("Server %s has allocation: %d replicas of %s (cost=%.2f)",
							server.Name(),
							alloc.NumReplicas(),
							alloc.Accelerator(),
							alloc.Cost())

						// Verify allocation is valid
						if alloc.NumReplicas() <= 0 {
							t.Errorf("Invalid allocation: replicas=%d", alloc.NumReplicas())
						}
						if alloc.Cost() <= 0 {
							t.Errorf("Invalid allocation: cost=%.2f", alloc.Cost())
						}
					}
				}

				if allocatedCount == 0 {
					t.Error("Optimize() should have created allocations for servers with valid system setup")
				}
			},
		},
		{
			name: "optimization with unlimited resources",
			setupManager: func() *Manager {
				system := createTestSystem()
				optimizerSpec := &config.OptimizerSpec{
					Unlimited:        true,
					SaturationPolicy: "PriorityExhaustive",
				}
				optimizer := solver.NewOptimizerFromSpec(optimizerSpec)
				manager := NewManager(system, optimizer)
				system.Calculate()

				return manager
			},
			wantErr: false,
			checkResult: func(t *testing.T, manager *Manager) {
				// With unlimited resources, optimization should succeed
				solutionTime := manager.optimizer.SolutionTimeMsec()
				if solutionTime < 0 {
					t.Error("Optimize() should have set solution time >= 0")
				}

				// Verify servers have allocations
				servers := manager.system.Servers()
				allocatedCount := 0
				for _, server := range servers {
					if server.Allocation() != nil {
						allocatedCount++
					}
				}

				t.Logf("Unlimited optimization: %d/%d servers allocated in %d msec",
					allocatedCount, len(servers), solutionTime)

				// With unlimited resources and a valid system setup, all servers MUST get allocations
				if len(servers) > 0 && allocatedCount == 0 {
					t.Error("Optimize() with unlimited resources should allocate all servers")
				}
			},
		},
		{
			name: "optimization with minimal empty system",
			setupManager: func() *Manager {
				system := core.NewSystem()
				optimizerSpec := &config.OptimizerSpec{
					Unlimited:        false,
					SaturationPolicy: "None",
				}
				optimizer := solver.NewOptimizerFromSpec(optimizerSpec)
				return NewManager(system, optimizer)
			},
			wantErr: false,
			checkResult: func(t *testing.T, manager *Manager) {
				// Even with empty system, optimization should complete without error
				if manager.system == nil {
					t.Error("System should not be nil after optimization")
				}

				// Solution time should still be set
				if manager.optimizer.SolutionTimeMsec() < 0 {
					t.Error("Solution time should be set even for empty system")
				}

				// Empty system should have no server allocations
				servers := manager.system.Servers()
				if len(servers) != 0 {
					t.Errorf("Expected 0 servers in empty system, got %d", len(servers))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := tt.setupManager()
			err := manager.Optimize()

			if (err != nil) != tt.wantErr {
				t.Errorf("Manager.Optimize() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.checkResult != nil {
				tt.checkResult(t, manager)
			}
		})
	}
}

func TestManager_OptimizeIntegration(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (*core.System, *solver.Optimizer)
		wantErr bool
	}{
		{
			name: "integration with minimal system",
			setup: func() (*core.System, *solver.Optimizer) {
				system := &core.System{}

				optimizerSpec := &config.OptimizerSpec{
					Unlimited:        false,
					SaturationPolicy: "None",
				}
				optimizer := solver.NewOptimizerFromSpec(optimizerSpec)

				return system, optimizer
			},
			wantErr: false,
		},
		{
			name: "integration with unlimited resources",
			setup: func() (*core.System, *solver.Optimizer) {
				system := &core.System{}

				optimizerSpec := &config.OptimizerSpec{
					Unlimited:        true,
					SaturationPolicy: "PriorityExhaustive",
				}
				optimizer := solver.NewOptimizerFromSpec(optimizerSpec)

				return system, optimizer
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			system, optimizer := tt.setup()
			manager := NewManager(system, optimizer)

			err := manager.Optimize()
			if (err != nil) != tt.wantErr {
				t.Errorf("Manager.Optimize() integration error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManager_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		manager *Manager
		wantErr bool
	}{
		{
			name: "manager with nil system",
			manager: &Manager{
				system:    nil,
				optimizer: &solver.Optimizer{},
			},
			wantErr: true,
		},
		{
			name: "manager with nil optimizer",
			manager: &Manager{
				system:    &core.System{},
				optimizer: nil,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil && !tt.wantErr {
					t.Errorf("Manager.Optimize() panicked: %v", r)
				}
			}()

			err := tt.manager.Optimize()
			if (err != nil) != tt.wantErr {
				t.Errorf("Manager.Optimize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
