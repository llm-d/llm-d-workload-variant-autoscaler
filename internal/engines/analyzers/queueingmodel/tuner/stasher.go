package tuner

import (
	"fmt"

	"gonum.org/v1/gonum/mat"

	kalman "github.com/llm-inferno/kalman-filter/pkg/core"
)

// Stasher is a helper to take a snapshot of the state of the filter and restore it later.
type Stasher struct {
	Filter *kalman.ExtendedKalmanFilter
	X      *mat.VecDense // State vector (Xdim)
	P      *mat.Dense    // Estimate uncertainty covariance (Xdim x Xdim)
}

func NewStasher(filter *kalman.ExtendedKalmanFilter) (*Stasher, error) {
	if filter == nil {
		return nil, fmt.Errorf("cannot create stasher: filter is nil")
	}
	return &Stasher{
		Filter: filter,
	}, nil
}

// copy X and P from filter to the stasher
func (s *Stasher) Stash() error {
	if s.Filter.X == nil || s.Filter.P == nil {
		return fmt.Errorf("filter state is not initialized")
	}
	s.X = mat.VecDenseCopyOf(s.Filter.X)
	s.P = mat.DenseCopyOf(s.Filter.P)
	return nil
}

// copy X and P from stasher to filter
func (s *Stasher) UnStash() error {
	if s.X == nil || s.P == nil {
		return fmt.Errorf("stasher state is not initialized")
	}
	s.Filter.X = mat.VecDenseCopyOf(s.X)
	s.Filter.P = mat.DenseCopyOf(s.P)
	return nil
}
