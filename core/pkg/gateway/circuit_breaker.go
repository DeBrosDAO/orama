package gateway

import (
	"fmt"
	"net/http"
	"sort"
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

	// breakerCycleLogInterval is how often a breaker that keeps failing its
	// probes (open, half-open, open, ...) says so. A breaker opens once and
	// stays down for as long as its target is dead, probing every
	// defaultOpenDuration; logging every probe would write two lines per
	// breaker per half minute, for every namespace on a dead node. The opening
	// and the closing are always logged.
	breakerCycleLogInterval = 5 * time.Minute

	// breakerKeyNamespaceGateway prefixes the key of a namespace gateway's
	// breaker. It is the only place the format is written.
	breakerKeyNamespaceGateway = "ns:"
)

// namespaceBreakerKey is the registry key of the breaker for one namespace's
// gateway on one node: one gateway process. A node hosts the gateways of many
// namespaces, and a namespace's gateway failing says nothing about another's,
// so the key names both.
func namespaceBreakerKey(namespace, nodeIP string) string {
	return breakerKeyNamespaceGateway + namespace + "@" + nodeIP
}

// BreakerTransition is one change of a breaker's state, handed to the
// registry's observer.
type BreakerTransition struct {
	Key       string
	Namespace string // empty for a breaker that is not a namespace gateway's
	Node      string
	From, To  CircuitState
	// Failures is the consecutive failure count when the state changed (before
	// the reset, for a close).
	Failures int
	// LastError is the reason of the most recent failure.
	LastError string
	// SuppressedCycles is how many failed probe cycles since the last logged
	// one were left out (breakerCycleLogInterval).
	SuppressedCycles int
}

// BreakerStatus is the state of one breaker that is not closed.
type BreakerStatus struct {
	Namespace   string
	Node        string
	State       CircuitState
	Failures    int
	LastError   string
	LastFailure time.Time
}

// CircuitBreaker implements the circuit breaker pattern per target.
type CircuitBreaker struct {
	mu               sync.Mutex
	key              string
	namespace        string
	node             string
	observer         func(BreakerTransition)
	state            CircuitState
	failures         int
	lastError        string
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
	// lastCycleLog, cycleQuiet and suppressedCycles rate-limit the log lines of
	// a breaker that keeps failing its probes (breakerCycleLogInterval).
	lastCycleLog     time.Time
	cycleQuiet       bool
	suppressedCycles int
}

// NewCircuitBreaker creates a circuit breaker with default settings.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: defaultFailureThreshold,
		openDuration:     defaultOpenDuration,
		halfOpenTimeout:  defaultHalfOpenTimeout,
	}
}

// transitionLocked moves the breaker to state to and returns what to tell the
// observer, or nil when nothing changed or the cycle is being rate-limited.
// The caller holds cb.mu and delivers the result after releasing it.
func (cb *CircuitBreaker) transitionLocked(to CircuitState, now time.Time) *BreakerTransition {
	from := cb.state
	if from == to {
		return nil
	}
	cb.state = to
	tr := &BreakerTransition{
		Key: cb.key, Namespace: cb.namespace, Node: cb.node,
		From: from, To: to, Failures: cb.failures, LastError: cb.lastError,
	}
	switch {
	case from == CircuitClosed && to == CircuitOpen:
		cb.lastCycleLog, cb.cycleQuiet = time.Time{}, false
	case from == CircuitOpen && to == CircuitHalfOpen:
		if !cb.lastCycleLog.IsZero() && now.Sub(cb.lastCycleLog) < breakerCycleLogInterval {
			cb.cycleQuiet = true
			cb.suppressedCycles++
			return nil
		}
		cb.cycleQuiet, cb.lastCycleLog = false, now
		tr.SuppressedCycles, cb.suppressedCycles = cb.suppressedCycles, 0
	case from == CircuitHalfOpen && to == CircuitOpen:
		if cb.cycleQuiet {
			return nil
		}
	case to == CircuitClosed:
		tr.SuppressedCycles, cb.suppressedCycles = cb.suppressedCycles, 0
		cb.cycleQuiet = false
	}
	return tr
}

func (cb *CircuitBreaker) notify(tr *BreakerTransition) {
	if tr != nil && cb.observer != nil {
		cb.observer(*tr)
	}
}

// Allow checks whether a request should be allowed through.
// Returns false if the circuit is open (fast-fail).
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	now := time.Now()
	cb.lastUsed = now
	allowed, tr := cb.allowLocked(now)
	cb.mu.Unlock()
	cb.notify(tr)
	return allowed
}

func (cb *CircuitBreaker) allowLocked(now time.Time) (bool, *BreakerTransition) {
	switch cb.state {
	case CircuitClosed:
		return true, nil
	case CircuitOpen:
		if now.Sub(cb.lastFailure) >= cb.openDuration {
			cb.probeStarted = now
			return true, cb.transitionLocked(CircuitHalfOpen, now)
		}
		return false, nil
	case CircuitHalfOpen:
		// One probe at a time: a half-open target is being tested by the
		// request already in flight, and sending it more would defeat the
		// test. If that probe never reported an outcome, treat the target as
		// still failing and re-open rather than holding the slot forever - a
		// latched half-open silently removes a healthy node from the
		// round-robin.
		if now.Sub(cb.probeStarted) >= cb.halfOpenTimeout {
			cb.lastError = fmt.Sprintf("the probe reported no outcome within %s", cb.halfOpenTimeout)
			cb.lastFailure = now
			cb.probeStarted = time.Time{}
			return false, cb.transitionLocked(CircuitOpen, now)
		}
		return false, nil
	}
	return true, nil
}

// RecordSuccess records a successful response, resetting the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	now := time.Now()
	tr := cb.transitionLocked(CircuitClosed, now)
	cb.failures = 0
	cb.lastError = ""
	cb.probeStarted = time.Time{}
	cb.lastUsed = now
	cb.mu.Unlock()
	cb.notify(tr)
}

// RecordFailure records a response that proved the target unhealthy, with the
// reason, potentially opening the circuit. Only a target-side fault is a
// failure: a client that left, a 4xx or a function's own error says nothing
// about the target and must be reported with neither this nor RecordSuccess
// (see Abandon).
func (cb *CircuitBreaker) RecordFailure(reason string) {
	cb.mu.Lock()
	now := time.Now()
	cb.failures++
	cb.lastFailure = now
	cb.lastError = reason
	cb.probeStarted = time.Time{}
	cb.lastUsed = now
	var tr *BreakerTransition
	// A failed half-open probe re-opens immediately: the target has just proven
	// it is still unhealthy, so waiting for the full threshold again would send
	// it four more doomed requests.
	if cb.state == CircuitHalfOpen || cb.failures >= cb.failureThreshold {
		tr = cb.transitionLocked(CircuitOpen, now)
	}
	cb.mu.Unlock()
	cb.notify(tr)
}

// Abandon reports that a request admitted by Allow ended without telling
// anything about the target: the client went away, or the request failed on
// this gateway's side. If the request held the half-open probe slot the slot is
// released at once, so the next caller probes instead of waiting out
// halfOpenTimeout. In any other state it does nothing. It may release a probe
// that belongs to another request when this one was admitted before the
// breaker opened; that only admits a second probe.
func (cb *CircuitBreaker) Abandon() {
	cb.mu.Lock()
	var tr *BreakerTransition
	if cb.state == CircuitHalfOpen {
		cb.probeStarted = time.Time{}
		tr = cb.transitionLocked(CircuitOpen, time.Now())
	}
	cb.mu.Unlock()
	cb.notify(tr)
}

// State reports the current state, for tests and diagnostics.
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
	observer func(BreakerTransition)
}

// NewCircuitBreakerRegistry creates a new registry.
func NewCircuitBreakerRegistry() *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: make(map[string]*CircuitBreaker),
	}
}

// SetObserver sets the function every breaker created from now on tells about
// its state changes. It is called outside the breaker's lock, from the request
// that caused the change.
func (r *CircuitBreakerRegistry) SetObserver(fn func(BreakerTransition)) {
	r.mu.Lock()
	r.observer = fn
	r.mu.Unlock()
}

// Get returns (or creates) a circuit breaker for the given target key.
func (r *CircuitBreakerRegistry) Get(target string) *CircuitBreaker {
	return r.get(target, "", target)
}

// ForNamespaceGateway returns (or creates) the breaker for the gateway of
// namespace on the node nodeIP.
func (r *CircuitBreakerRegistry) ForNamespaceGateway(namespace, nodeIP string) *CircuitBreaker {
	return r.get(namespaceBreakerKey(namespace, nodeIP), namespace, nodeIP)
}

func (r *CircuitBreakerRegistry) get(key, namespace, node string) *CircuitBreaker {
	r.mu.RLock()
	cb, ok := r.breakers[key]
	r.mu.RUnlock()
	if ok {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock
	if cb, ok = r.breakers[key]; ok {
		return cb
	}
	cb = NewCircuitBreaker()
	cb.key, cb.namespace, cb.node, cb.observer = key, namespace, node, r.observer
	cb.lastUsed = time.Now()
	r.breakers[key] = cb
	return cb
}

// Len is how many breakers the registry holds.
func (r *CircuitBreakerRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.breakers)
}

// RetainNamespaceMembers drops the breakers of namespace's gateways on nodes
// that are not in nodeIPs, which is the namespace's current member list: a
// namespace that was removed (an empty list) loses all of them, one that moved
// off a node loses that node's. Breakers of other namespaces and of other kinds
// of target are left alone. Returns how many were dropped.
func (r *CircuitBreakerRegistry) RetainNamespaceMembers(namespace string, nodeIPs []string) int {
	keep := make(map[string]struct{}, len(nodeIPs))
	for _, ip := range nodeIPs {
		keep[namespaceBreakerKey(namespace, ip)] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for k, cb := range r.breakers {
		if cb.namespace != namespace || cb.namespace == "" {
			continue
		}
		if _, ok := keep[k]; !ok {
			delete(r.breakers, k)
			dropped++
		}
	}
	return dropped
}

// Prune drops breakers that have seen no traffic for maxIdle.
//
// The registry grew without bound: a node removed from the cluster left its
// breaker behind forever, as did every namespace ever proxied. RetainNamespaceMembers
// drops the breakers of a member list that changed as soon as the proxy learns
// of it; Prune is the backstop for a namespace nobody asks about again. Pruning
// by idleness rather than by "keep exactly this set" means no call site can
// evict a breaker another path is still using. Returns how many were dropped.
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

// Unhealthy returns the breakers that are not closed and have seen traffic
// within maxIdle, ordered by namespace then node, for the node's telemetry
// report. A breaker nobody has asked about for longer (its namespace was
// deleted, or has no traffic) stays open until it is pruned, but it is
// refusing nobody, so it is not a fault to report.
func (r *CircuitBreakerRegistry) Unhealthy(maxIdle time.Duration) []BreakerStatus {
	cutoff := time.Now().Add(-maxIdle)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []BreakerStatus
	for _, cb := range r.breakers {
		cb.mu.Lock()
		if cb.state != CircuitClosed && !cb.lastUsed.Before(cutoff) {
			out = append(out, BreakerStatus{
				Namespace: cb.namespace, Node: cb.node, State: cb.state,
				Failures: cb.failures, LastError: cb.lastError, LastFailure: cb.lastFailure,
			})
		}
		cb.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Node < out[j].Node
	})
	return out
}
