package gateway

import (
	"net/http"
	"sync"
	"time"
)

// CircuitState represents the current state of a circuit breaker
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // Normal operation
	CircuitOpen                         // Fast-failing
	CircuitHalfOpen                     // Probing with a single request
)

const (
	defaultFailureThreshold = 5
	defaultOpenDuration     = 30 * time.Second
)

// CircuitBreaker implements the circuit breaker pattern per target.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            CircuitState
	failures         int
	failureThreshold int
	lastFailure      time.Time
	openDuration     time.Duration
}

// NewCircuitBreaker creates a circuit breaker with default settings.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: defaultFailureThreshold,
		openDuration:     defaultOpenDuration,
	}
}

// Allow checks whether a request should be allowed through.
// Returns false if the circuit is open (fast-fail).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) >= cb.openDuration {
			cb.state = CircuitHalfOpen
			return true
		}
		return false
	case CircuitHalfOpen:
		// Only one probe at a time — already in half-open means one is in flight
		return false
	}
	return true
}

// RecordSuccess records a successful response, resetting the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = CircuitClosed
}

// RecordFailure records a failed response, potentially opening the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cb.failureThreshold {
		cb.state = CircuitOpen
	}
}

// IsResponseFailure checks if an HTTP response status indicates a backend failure
// that should count toward the circuit breaker threshold.
func IsResponseFailure(statusCode int) bool {
	return statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

// CircuitBreakerRegistry manages per-target circuit breakers.
type CircuitBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

// NewCircuitBreakerRegistry creates a new registry.
func NewCircuitBreakerRegistry() *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// Get returns (or creates) a circuit breaker for the given target key.
func (r *CircuitBreakerRegistry) Get(target string) *CircuitBreaker {
	r.mu.RLock()
	cb, ok := r.breakers[target]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock
	if cb, ok = r.breakers[target]; ok {
		return cb
	}
	cb = NewCircuitBreaker()
	r.breakers[target] = cb
	return cb
}
