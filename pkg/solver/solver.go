package solver

import (
	"bytes"
	"fmt"
	"math"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/config"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/core"
)

// Solver of allocation assignment problem
type Solver struct {
	optimizerSpec *config.OptimizerSpec

	// current allocation for all servers
	currentAllocation map[string]*core.Allocation

	// difference in allocation for all servers
	diffAllocation map[string]*core.AllocationDiff
}

func NewSolver(optimizerSpec *config.OptimizerSpec) *Solver {
	return &Solver{
		optimizerSpec:     optimizerSpec,
		currentAllocation: make(map[string]*core.Allocation),
		diffAllocation:    make(map[string]*core.AllocationDiff),
	}
}

// Find optimal allocation for all service classes
func (s *Solver) Solve() error {
	// take snapshot of current allocations
	s.currentAllocation = make(map[string]*core.Allocation)
	for serverName, server := range core.GetServers() {
		if alloc := server.CurAllocation(); alloc != nil {
			s.currentAllocation[serverName] = alloc
		}
	}

	// find solution
	if s.optimizerSpec.Unlimited {
		s.SolveUnlimited()
	} else {
		s.SolveGreedy()
	}

	// TODO: cleanup after trying MIP solver

	s.diffAllocation = make(map[string]*core.AllocationDiff)
	for serverName, server := range core.GetServers() {
		curAlloc := s.currentAllocation[serverName]
		desiredAlloc := server.Allocation()
		if allocDiff := core.CreateAllocationDiff(curAlloc, desiredAlloc); allocDiff != nil {
			s.diffAllocation[serverName] = allocDiff
		}
	}
	return nil
}

// Find optimal allocations assuming unlimited accelerator capacity
// (separable objective function: best allocation for each server)
func (s *Solver) SolveUnlimited() {
	for _, server := range core.GetServers() {
		server.RemoveAllocation()
		// select allocation with minimum value
		minVal := float32(math.MaxFloat32)
		var minAlloc *core.Allocation
		for _, alloc := range server.AllAllocations() {
			if alloc.Value() < minVal {
				minVal = alloc.Value()
				minAlloc = alloc
			}
		}
		if minAlloc != nil {
			server.SetAllocation(minAlloc)
		}
	}
}

func (s *Solver) AllocationDiff() map[string]*core.AllocationDiff {
	return s.diffAllocation
}

func (s *Solver) String() string {
	var b bytes.Buffer
	b.WriteString("Solver: \n")
	for serverName, allocDiff := range s.diffAllocation {
		fmt.Fprintf(&b, "sName=%s, allocDiff=%v \n",
			serverName, allocDiff)
	}
	return b.String()
}
