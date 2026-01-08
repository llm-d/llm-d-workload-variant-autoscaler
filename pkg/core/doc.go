// Package core provides fundamental data structures and business logic for the WVA optimization engine.
//
// This package contains the core domain models that represent the entities and relationships
// in the autoscaling system:
//
//   - Accelerator: GPU/accelerator types and their specifications
//   - Model: LLM model definitions and characteristics
//   - Server: Inference server instances with capacity and performance metrics
//   - ServiceClass: QoS requirements and SLO definitions
//   - Allocation: Resource allocation decisions and optimizations
//   - System: Global system state and multi-server coordination
//
// These types form the foundation for the optimization algorithms in the solver package
// and are used throughout the controller for decision-making.
//
// Example usage:
//
//	// Create an accelerator definition
//	accel := core.NewAccelerator("NVIDIA-H100-80GB", 80*1024, 100.0)
//
//	// Define a model
//	model := core.NewModel("meta/llama-3.1-8b", 8*1024, 4096, 128000)
//
//	// Create a server with capacity
//	server := core.NewServer(model, accel, 32, 1000)
//
//	// Define service class requirements
//	sc := core.NewServiceClass("premium", 500, 100, 50)
//
//	// Create an allocation
//	alloc := core.NewAllocation(server, 8, sc)
//
// The core package is designed to be:
//   - Immutable where possible (value types)
//   - Type-safe with strong domain boundaries
//   - Independent of Kubernetes APIs (pure domain logic)
//   - Well-tested with comprehensive unit tests
package core
