package alerting

import (
	"errors"
	"sync"
)

var ErrInvalidPolicy = errors.New("alert policy is invalid")

type Policy struct {
	Threshold float64
}

type Evaluator struct {
	policy Policy
	mu     sync.Mutex
	active map[string]bool
}

func New(policy Policy) (*Evaluator, error) {
	if policy.Threshold <= 0 {
		return nil, ErrInvalidPolicy
	}
	return &Evaluator{policy: policy, active: make(map[string]bool)}, nil
}

// Observe returns true exactly once when a series enters a breached state.
// Returning below the threshold rearms the series for a future breach.
func (e *Evaluator) Observe(series string, value float64) (bool, error) {
	if series == "" {
		return false, ErrInvalidPolicy
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if value < e.policy.Threshold {
		e.active[series] = false
		return false, nil
	}
	if e.active[series] {
		return false, nil
	}
	e.active[series] = true
	return true, nil
}
