package core

import (
	"strings"
	"testing"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/config"
)

func TestNewServiceClass(t *testing.T) {
	tests := []struct {
		name             string
		className        string
		priority         int
		expectedName     string
		expectedPriority int
	}{
		{
			name:             "valid service class",
			className:        "high-priority",
			priority:         1,
			expectedName:     "high-priority",
			expectedPriority: 1,
		},
		{
			name:             "priority too low (gets default)",
			className:        "test-class",
			priority:         -1,
			expectedName:     "test-class",
			expectedPriority: config.DefaultServiceClassPriority,
		},
		{
			name:             "priority too high (gets default)",
			className:        "test-class",
			priority:         150, // Above DefaultLowPriority (100)
			expectedName:     "test-class",
			expectedPriority: config.DefaultServiceClassPriority,
		},
		{
			name:             "empty class name",
			className:        "",
			priority:         5,
			expectedName:     "",
			expectedPriority: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewServiceClass(tt.className, tt.priority)

			if svc.Name() != tt.expectedName {
				t.Errorf("NewServiceClass().Name() = %v, want %v", svc.Name(), tt.expectedName)
			}
			if svc.Priority() != tt.expectedPriority {
				t.Errorf("NewServiceClass().Priority() = %v, want %v", svc.Priority(), tt.expectedPriority)
			}
			if svc.targets == nil {
				t.Error("NewServiceClass() should initialize targets map")
			}
		})
	}
}

func TestNewServiceClassFromSpec(t *testing.T) {
	tests := []struct {
		name string
		spec *config.ServiceClassSpec
		want *ServiceClass
	}{
		{
			name: "service class with model targets",
			spec: &config.ServiceClassSpec{
				Name:     "high-priority",
				Priority: 1,
				ModelTargets: []config.ModelTarget{
					{
						Model:    "model-1",
						SLO_ITL:  50.0,
						SLO_TTFT: 100.0,
						SLO_TPS:  10.0,
					},
					{
						Model:    "model-2",
						SLO_ITL:  75.0,
						SLO_TTFT: 150.0,
						SLO_TPS:  5.0,
					},
				},
			},
			want: &ServiceClass{
				name:     "high-priority",
				priority: 1,
			},
		},
		{
			name: "service class with no model targets",
			spec: &config.ServiceClassSpec{
				Name:         "basic-class",
				Priority:     8,
				ModelTargets: []config.ModelTarget{},
			},
			want: &ServiceClass{
				name:     "basic-class",
				priority: 8,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewServiceClassFromSpec(tt.spec)

			if svc.Name() != tt.want.name {
				t.Errorf("NewServiceClassFromSpec().Name() = %v, want %v", svc.Name(), tt.want.name)
			}
			if svc.Priority() != tt.want.priority {
				t.Errorf("NewServiceClassFromSpec().Priority() = %v, want %v", svc.Priority(), tt.want.priority)
			}

			// Check that model targets were added
			for _, modelTarget := range tt.spec.ModelTargets {
				target := svc.ModelTarget(modelTarget.Model)
				if target == nil {
					t.Errorf("NewServiceClassFromSpec() missing target for model %s", modelTarget.Model)
					continue
				}
				if target.ITL != modelTarget.SLO_ITL {
					t.Errorf("Target ITL = %v, want %v", target.ITL, modelTarget.SLO_ITL)
				}
				if target.TTFT != modelTarget.SLO_TTFT {
					t.Errorf("Target TTFT = %v, want %v", target.TTFT, modelTarget.SLO_TTFT)
				}
				if target.TPS != modelTarget.SLO_TPS {
					t.Errorf("Target TPS = %v, want %v", target.TPS, modelTarget.SLO_TPS)
				}
			}
		})
	}
}

func TestServiceClass_Getters(t *testing.T) {
	svc := NewServiceClass("test-class", 3)

	// Add a model target
	target := &Target{ITL: 50.0, TTFT: 100.0, TPS: 10.0}
	svc.targets["test-model"] = target

	tests := []struct {
		name     string
		getter   func() interface{}
		expected interface{}
	}{
		{
			name:     "Name",
			getter:   func() interface{} { return svc.Name() },
			expected: "test-class",
		},
		{
			name:     "Priority",
			getter:   func() interface{} { return svc.Priority() },
			expected: 3,
		},
		{
			name:     "ModelTarget existing",
			getter:   func() interface{} { return svc.ModelTarget("test-model") },
			expected: target,
		},
		{
			name:     "ModelTarget nonexistent",
			getter:   func() interface{} { return svc.ModelTarget("nonexistent-model") },
			expected: (*Target)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.getter()
			if result != tt.expected {
				t.Errorf("%s() = %v, want %v", tt.name, result, tt.expected)
			}
		})
	}
}

func TestServiceClass_AddModelTarget(t *testing.T) {
	svc := NewServiceClass("test-class", 5)

	tests := []struct {
		name string
		spec *config.ModelTarget
	}{
		{
			name: "add new model target",
			spec: &config.ModelTarget{
				Model:    "model-1",
				SLO_ITL:  50.0,
				SLO_TTFT: 100.0,
				SLO_TPS:  10.0,
			},
		},
		{
			name: "replace existing model target",
			spec: &config.ModelTarget{
				Model:    "model-1", // Same model, different values
				SLO_ITL:  75.0,
				SLO_TTFT: 150.0,
				SLO_TPS:  5.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := svc.AddModelTarget(tt.spec)

			// Check returned target
			if target == nil || tt.spec == nil {
				t.Fatal("AddModelTarget() should return a target")
			}
			if target.ITL != tt.spec.SLO_ITL {
				t.Errorf("AddModelTarget() ITL = %v, want %v", target.ITL, tt.spec.SLO_ITL)
			}
			if target.TTFT != tt.spec.SLO_TTFT {
				t.Errorf("AddModelTarget() TTFT = %v, want %v", target.TTFT, tt.spec.SLO_TTFT)
			}
			if target.TPS != tt.spec.SLO_TPS {
				t.Errorf("AddModelTarget() TPS = %v, want %v", target.TPS, tt.spec.SLO_TPS)
			}

			// Check that target is stored in service class
			storedTarget := svc.ModelTarget(tt.spec.Model)
			if storedTarget != target {
				t.Error("AddModelTarget() should store the target in the service class")
			}
		})
	}
}

func TestServiceClass_RemoveModelTarget(t *testing.T) {
	svc := NewServiceClass("test-class", 5)

	// Add some targets
	svc.AddModelTarget(&config.ModelTarget{
		Model:    "model-1",
		SLO_ITL:  50.0,
		SLO_TTFT: 100.0,
		SLO_TPS:  10.0,
	})
	svc.AddModelTarget(&config.ModelTarget{
		Model:    "model-2",
		SLO_ITL:  75.0,
		SLO_TTFT: 150.0,
		SLO_TPS:  5.0,
	})

	tests := []struct {
		name              string
		modelName         string
		shouldExistBefore bool
		shouldExistAfter  bool
	}{
		{
			name:              "remove existing target",
			modelName:         "model-1",
			shouldExistBefore: true,
			shouldExistAfter:  false,
		},
		{
			name:              "remove nonexistent target",
			modelName:         "nonexistent-model",
			shouldExistBefore: false,
			shouldExistAfter:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Check state before removal
			targetBefore := svc.ModelTarget(tt.modelName)
			if (targetBefore != nil) != tt.shouldExistBefore {
				t.Errorf("Before removal: ModelTarget(%s) exists = %v, want %v",
					tt.modelName, targetBefore != nil, tt.shouldExistBefore)
			}

			// Remove the target
			svc.RemoveModelTarget(tt.modelName)

			// Check state after removal
			targetAfter := svc.ModelTarget(tt.modelName)
			if (targetAfter != nil) != tt.shouldExistAfter {
				t.Errorf("After removal: ModelTarget(%s) exists = %v, want %v",
					tt.modelName, targetAfter != nil, tt.shouldExistAfter)
			}
		})
	}
}

func TestServiceClass_UpdateModelTargets(t *testing.T) {
	svc := NewServiceClass("test-class", 5)

	tests := []struct {
		name     string
		spec     *config.ServiceClassSpec
		expected bool
	}{
		{
			name: "update with matching name and priority",
			spec: &config.ServiceClassSpec{
				Name:     "test-class",
				Priority: 5,
				ModelTargets: []config.ModelTarget{
					{
						Model:    "model-1",
						SLO_ITL:  50.0,
						SLO_TTFT: 100.0,
						SLO_TPS:  10.0,
					},
				},
			},
			expected: true,
		},
		{
			name: "update with different name",
			spec: &config.ServiceClassSpec{
				Name:         "different-class",
				Priority:     5,
				ModelTargets: []config.ModelTarget{},
			},
			expected: false,
		},
		{
			name: "update with different priority",
			spec: &config.ServiceClassSpec{
				Name:         "test-class",
				Priority:     8,
				ModelTargets: []config.ModelTarget{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.UpdateModelTargets(tt.spec)

			if result != tt.expected {
				t.Errorf("UpdateModelTargets() = %v, want %v", result, tt.expected)
			}

			if tt.expected {
				// Check that targets were updated
				for _, modelTarget := range tt.spec.ModelTargets {
					target := svc.ModelTarget(modelTarget.Model)
					if target == nil {
						t.Errorf("UpdateModelTargets() should add target for model %s", modelTarget.Model)
						continue
					}
					if target.ITL != modelTarget.SLO_ITL {
						t.Errorf("Target ITL = %v, want %v", target.ITL, modelTarget.SLO_ITL)
					}
				}
			}
		})
	}
}

func TestServiceClass_Spec(t *testing.T) {
	svc := NewServiceClass("test-class", 3)

	// Add some model targets
	svc.AddModelTarget(&config.ModelTarget{
		Model:    "model-1",
		SLO_ITL:  50.0,
		SLO_TTFT: 100.0,
		SLO_TPS:  10.0,
	})
	svc.AddModelTarget(&config.ModelTarget{
		Model:    "model-2",
		SLO_ITL:  75.0,
		SLO_TTFT: 150.0,
		SLO_TPS:  5.0,
	})

	spec := svc.Spec()

	// Check basic properties
	if spec.Name != "test-class" {
		t.Errorf("Spec().Name = %v, want test-class", spec.Name)
	}
	if spec.Priority != 3 {
		t.Errorf("Spec().Priority = %v, want 3", spec.Priority)
	}

	// Check model targets
	if len(spec.ModelTargets) != 2 {
		t.Errorf("Spec().ModelTargets length = %v, want 2", len(spec.ModelTargets))
	}

	// Verify model targets (order might vary)
	modelTargetMap := make(map[string]config.ModelTarget)
	for _, target := range spec.ModelTargets {
		modelTargetMap[target.Model] = target
	}

	expectedTargets := map[string]config.ModelTarget{
		"model-1": {Model: "model-1", SLO_ITL: 50.0, SLO_TTFT: 100.0, SLO_TPS: 10.0},
		"model-2": {Model: "model-2", SLO_ITL: 75.0, SLO_TTFT: 150.0, SLO_TPS: 5.0},
	}

	for modelName, expectedTarget := range expectedTargets {
		if actualTarget, exists := modelTargetMap[modelName]; !exists {
			t.Errorf("Spec() missing model target for %s", modelName)
		} else {
			if actualTarget.SLO_ITL != expectedTarget.SLO_ITL {
				t.Errorf("Spec() model %s ITL = %v, want %v", modelName, actualTarget.SLO_ITL, expectedTarget.SLO_ITL)
			}
			if actualTarget.SLO_TTFT != expectedTarget.SLO_TTFT {
				t.Errorf("Spec() model %s TTFT = %v, want %v", modelName, actualTarget.SLO_TTFT, expectedTarget.SLO_TTFT)
			}
			if actualTarget.SLO_TPS != expectedTarget.SLO_TPS {
				t.Errorf("Spec() model %s TPS = %v, want %v", modelName, actualTarget.SLO_TPS, expectedTarget.SLO_TPS)
			}
		}
	}
}

func TestServiceClass_String(t *testing.T) {
	svc := NewServiceClass("test-class", 5)

	// Add a model target
	svc.AddModelTarget(&config.ModelTarget{
		Model:    "test-model",
		SLO_ITL:  50.0,
		SLO_TTFT: 100.0,
		SLO_TPS:  10.0,
	})

	result := svc.String()

	// Check that string contains key information
	if !strings.Contains(result, "test-class") {
		t.Error("String() should contain service class name")
	}
	if !strings.Contains(result, "5") {
		t.Error("String() should contain priority")
	}
}

func TestTarget_String(t *testing.T) {
	target := &Target{
		ITL:  50.0,
		TTFT: 100.0,
		TPS:  10.0,
	}

	result := target.String()

	// Check that string contains the values
	if !strings.Contains(result, "50") {
		t.Error("Target String() should contain ITL value")
	}
	if !strings.Contains(result, "100") {
		t.Error("Target String() should contain TTFT value")
	}
	if !strings.Contains(result, "10") {
		t.Error("Target String() should contain TPS value")
	}
}

func TestServiceClass_Integration(t *testing.T) {
	// Test a complete workflow with multiple operations
	t.Run("complete workflow", func(t *testing.T) {
		// Create service class from spec
		spec := &config.ServiceClassSpec{
			Name:     "integration-test",
			Priority: 2,
			ModelTargets: []config.ModelTarget{
				{
					Model:    "model-1",
					SLO_ITL:  25.0,
					SLO_TTFT: 50.0,
					SLO_TPS:  20.0,
				},
			},
		}

		svc := NewServiceClassFromSpec(spec)

		// Add another target
		svc.AddModelTarget(&config.ModelTarget{
			Model:    "model-2",
			SLO_ITL:  40.0,
			SLO_TTFT: 80.0,
			SLO_TPS:  15.0,
		})

		// Update existing target
		svc.AddModelTarget(&config.ModelTarget{
			Model:    "model-1", // Replace model-1
			SLO_ITL:  30.0,
			SLO_TTFT: 60.0,
			SLO_TPS:  25.0,
		})

		// Verify final state
		if len(svc.targets) != 2 {
			t.Errorf("Expected 2 targets, got %d", len(svc.targets))
		}

		target1 := svc.ModelTarget("model-1")
		if target1 == nil || target1.ITL != 30.0 {
			t.Error("model-1 target should be updated")
		}

		target2 := svc.ModelTarget("model-2")
		if target2 == nil || target2.TPS != 15.0 {
			t.Error("model-2 target should be present")
		}

		// Remove one target
		svc.RemoveModelTarget("model-2")
		if len(svc.targets) != 1 {
			t.Errorf("Expected 1 target after removal, got %d", len(svc.targets))
		}

		// Get final spec
		finalSpec := svc.Spec()
		if len(finalSpec.ModelTargets) != 1 {
			t.Errorf("Final spec should have 1 model target, got %d", len(finalSpec.ModelTargets))
		}
	})
}
