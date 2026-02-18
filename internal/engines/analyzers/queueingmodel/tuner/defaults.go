package tuner

/**
 * Default filter parameters
 */

// infinitesimal value to check float equality
const DefaultEpsilon = 1e-6

// Default tuner parameters
const (
	/*
		Under nominal conditions, the NIS (Normalized Innovations Squared) of a Kalman Filter is expected to follow
		a Chi-Squared Distribution with degrees of freedom equal to the dimension of the measurement vector (n = 2 for [ttft, itl]).
		Here, we enforce that a tuner update is accepted for 95% confidence interval of NIS.
		The upper bound of the interval in our case is 7.378.
	*/
	DefaultMaxNIS = 7.378

	// State vector indices for model parameters
	StateIndexAlpha = 0 // base parameter
	StateIndexBeta  = 1 // compute slope parameter
	StateIndexGamma = 2 // memory access slope parameter
)
