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

	// defaultHalfOpenTimeout bounds how long a single probe may hold the
	// half-open slot. Allow() lets exactly one caller through and refuses
	// everyone else until that caller reports an outcome, so a caller that
	// never reports removes the target from rotation for the life of the
	// process. That is what made "restart orama-node to clear the breakers" the
	// documented cure in docs/NODE_REPLACEMENT.md.
	defaultHalfOpenTimeout = 30 * time.Second
)

// CircuitBreaker implements the circuit breaker pattern per target.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            CircuitState
	failures         int
	failureThreshold int
	lastFailure      time.Time
	openDuration     time.Duration
	halfOpenTimeout  time.Duration
	// probeStarted is when the current half-open probe was admitted. Zero
	// unless state is CircuitHalfOpen.
	probeStarted time.Time
	// lastUsed is when this breaker last saw traffic, so idle entries for
	// targets that no longer exist can be pruned.
	lastUsed time.Time
}

// NewCircuitBreaker creates a circuit breaker with default settings.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: defaultFailureThreshold,
		openDuration:     defaultOpenDuration,
		halfOpenTimeout:  defaultHalfOpenTimeout,
	}
}

// Allow checks whether a request should be allowed through.
// Returns false if the circuit is open (fast-fail).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.lastUsed = time.Now()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) >= cb.openDuration {
			cb.state = CircuitHalfOpen
			cb.probeStarted = time.Now()
			return true
		}
		return false
	case CircuitHalfOpen:
		// One probe at a time. If the in-flight probe never reported an
		// outcome, treat the target as still failing and re-open rather than
		// holding the slot forever - a latched half-open silently removes a
		// healthy node from the round-robin.
		if time.Since(cb.probeStarted) >= cb.halfOpenTimeout {
			cb.state = CircuitOpen
			cb.lastFailure = time.Now()
			cb.probeStarted = time.Time{}
		}
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
	cb.probeStarted = time.Time{}
	cb.lastUsed = time.Now()
}

// RecordFailure records a failed response, potentially opening the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	cb.probeStarted = time.Time{}
	cb.lastUsed = time.Now()
	// A failed half-open probe re-opens immediately: the target has just proven
	// it is still unhealthy, so waiting for the full threshold again would send
	// it four more doomed requests.
	if cb.state == CircuitHalfOpen || cb.failures >= cb.failureThreshold {
		cb.state = CircuitOpen
	}
}

// State reports the current state. For diagnostics - the breaker registry is
// otherwise invisible, which is why the runbook could only suggest a restart.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// String renders a state for logs and health output.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	}
	return "unknown"
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
	cb.lastUsed = time.Now()
	r.breakers[target] = cb
	return cb
}

// Prune drops breakers that have seen no traffic for maxIdle.
//
// The registry is keyed "ns:<ip>" / "node:<ip>" from several call sites and grew
// without bound: a node removed from the cluster left its breaker behind
// forever, as did every namespace ever proxied. Pruning by idleness rather than
// by "keep exactly this set" means no call site can evict a breaker another
// path is still using. Returns how many were dropped.
func (r *CircuitBreakerRegistry) Prune(maxIdle time.Duration) int {
	cutoff := time.Now().Add(-maxIdle)
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for k, cb := range r.breakers {
		cb.mu.Lock()
		idle := cb.lastUsed.Before(cutoff)
		cb.mu.Unlock()
		if idle {
			delete(r.breakers, k)
			dropped++
		}
	}
	return dropped
}

// Snapshot returns the current state of every breaker, for /v1/status.
func (r *CircuitBreakerRegistry) Snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.breakers))
	for k, cb := range r.breakers {
		out[k] = cb.State().String()
	}
	return out
}
