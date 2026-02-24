package redis

import (
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = 0 // Normal operation — requests pass through
	StateOpen     State = 1 // Circuit tripped — requests rejected immediately
	StateHalfOpen State = 2 // Testing — one request allowed through to probe
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements a simple circuit breaker pattern.
// After maxFailures consecutive failures, the breaker opens and rejects all
// calls for resetTimeout. After the timeout, it enters half-open state and
// allows one probe call through. If the probe succeeds, the breaker closes;
// if it fails, it reopens.
type CircuitBreaker struct {
	mu           sync.Mutex
	state        State
	failures     int
	maxFailures  int
	resetTimeout time.Duration
	lastFailure  time.Time

	// Callbacks (optional)
	OnStateChange func(from, to State) // called on state transitions
}

// NewCircuitBreaker creates a circuit breaker.
// maxFailures: consecutive failures before opening (e.g., 5)
// resetTimeout: time to wait before half-open probe (e.g., 10s)
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        StateClosed,
	}
}

// Execute runs fn through the circuit breaker.
// Returns ErrCircuitOpen if the breaker is open and the timeout hasn't elapsed.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()

	switch cb.state {
	case StateOpen:
		// Check if reset timeout has elapsed → transition to half-open
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.transition(StateHalfOpen)
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}

	case StateHalfOpen:
		// Allow the probe call through (only one at a time via mutex)
	}

	cb.mu.Unlock()

	// Execute the actual function
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()

		if cb.state == StateHalfOpen {
			// Probe failed — reopen
			cb.transition(StateOpen)
		} else if cb.failures >= cb.maxFailures {
			// Too many failures — trip the breaker
			cb.transition(StateOpen)
		}
		return err
	}

	// Success — reset
	if cb.state == StateHalfOpen {
		cb.transition(StateClosed)
	}
	cb.failures = 0
	return nil
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) CurrentState() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) transition(to State) {
	from := cb.state
	cb.state = to
	if to == StateClosed {
		cb.failures = 0
	}
	if cb.OnStateChange != nil {
		cb.OnStateChange(from, to)
	}
}

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = fmt.Errorf("circuit breaker is open")
