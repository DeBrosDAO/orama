package lifecycle

import (
	"fmt"
	"sync"
	"time"
)

// State represents a node's lifecycle state.
type State string

const (
	StateJoining State = "joining"
	StateActive  State = "active"
	// StateDegraded means the node's local services are up and serving, but at
	// least one component that needs the cluster — a raft leader, the schema,
	// the DNS tables — has not converged. It is a serving state, not a failure
	// state: a degraded node answers HTTPS, DNS and tenant traffic from its
	// local replicas, and returns to active on its own when the cluster comes
	// back. The boot supervisor drives a node in and out of it.
	StateDegraded    State = "degraded"
	StateDraining    State = "draining"
	StateMaintenance State = "maintenance"
)

// MaxMaintenanceTTL is the maximum duration a node can remain in maintenance
// mode. The leader's health monitor enforces this limit — nodes that exceed
// it are treated as unreachable so they can't hide in maintenance forever.
const MaxMaintenanceTTL = 15 * time.Minute

// validTransitions defines the allowed state machine transitions.
// Each entry maps from-state → set of valid to-states.
var validTransitions = map[State]map[State]bool{
	StateJoining:     {StateActive: true, StateDegraded: true},
	StateActive:      {StateDegraded: true, StateDraining: true, StateMaintenance: true},
	StateDegraded:    {StateActive: true, StateDraining: true, StateMaintenance: true},
	StateDraining:    {StateMaintenance: true},
	StateMaintenance: {StateActive: true, StateDegraded: true},
}

// StateChangeCallback is called when the lifecycle state changes.
type StateChangeCallback func(old, new State)

// Manager manages a node's lifecycle state machine.
// It has no external dependencies (no LibP2P, no discovery imports)
// and is fully testable in isolation.
type Manager struct {
	mu             sync.RWMutex
	state          State
	maintenanceTTL time.Time
	enterTime      time.Time // when the current state was entered
	onStateChange  []StateChangeCallback
}

// NewManager creates a new lifecycle manager in the joining state.
func NewManager() *Manager {
	return &Manager{
		state:     StateJoining,
		enterTime: time.Now(),
	}
}

// State returns the current lifecycle state.
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// MaintenanceTTL returns the maintenance mode expiration time.
// Returns zero value if not in maintenance.
func (m *Manager) MaintenanceTTL() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.maintenanceTTL
}

// StateEnteredAt returns when the current state was entered.
func (m *Manager) StateEnteredAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enterTime
}

// OnStateChange registers a callback invoked on state transitions.
// Callbacks are called with the lock released to avoid deadlocks.
func (m *Manager) OnStateChange(cb StateChangeCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = append(m.onStateChange, cb)
}

// TransitionTo moves the node to a new lifecycle state.
// Returns an error if the transition is not valid.
func (m *Manager) TransitionTo(newState State) error {
	return m.transition(newState, "")
}

// TransitionToFrom moves to newState only if the current state is still
// expected. It exists for callers that decided on a state they read earlier:
// the boot supervisor picks a target from a snapshot, and without the compare a
// concurrent EnterMaintenance on the shutdown path can land in between and have
// its announcement silently undone.
func (m *Manager) TransitionToFrom(expected, newState State) error {
	if expected == "" {
		return fmt.Errorf("lifecycle: TransitionToFrom needs an expected state")
	}
	return m.transition(newState, expected)
}

// transition applies newState. A non-empty expected state makes the change
// conditional on the current state still being that one.
func (m *Manager) transition(newState, expected State) error {
	m.mu.Lock()
	old := m.state

	if expected != "" && old != expected {
		m.mu.Unlock()
		return fmt.Errorf("lifecycle: state moved from %s to %s while %s was being decided", expected, old, newState)
	}

	allowed, exists := validTransitions[old]
	if !exists || !allowed[newState] {
		m.mu.Unlock()
		return fmt.Errorf("invalid lifecycle transition: %s → %s", old, newState)
	}

	m.state = newState
	m.enterTime = time.Now()

	// Clear maintenance TTL when leaving maintenance
	if newState != StateMaintenance {
		m.maintenanceTTL = time.Time{}
	}

	// Copy callbacks before releasing lock
	callbacks := make([]StateChangeCallback, len(m.onStateChange))
	copy(callbacks, m.onStateChange)
	m.mu.Unlock()

	// Invoke callbacks without holding the lock
	for _, cb := range callbacks {
		cb(old, newState)
	}

	return nil
}

// EnterMaintenance transitions to maintenance with a TTL.
// The TTL is capped at MaxMaintenanceTTL.
func (m *Manager) EnterMaintenance(ttl time.Duration) error {
	if ttl <= 0 {
		ttl = MaxMaintenanceTTL
	}
	if ttl > MaxMaintenanceTTL {
		ttl = MaxMaintenanceTTL
	}

	m.mu.Lock()
	old := m.state

	// Reachable from active, degraded and draining.
	allowed, exists := validTransitions[old]
	if !exists || !allowed[StateMaintenance] {
		m.mu.Unlock()
		return fmt.Errorf("invalid lifecycle transition: %s → %s", old, StateMaintenance)
	}

	m.state = StateMaintenance
	m.maintenanceTTL = time.Now().Add(ttl)
	m.enterTime = time.Now()

	callbacks := make([]StateChangeCallback, len(m.onStateChange))
	copy(callbacks, m.onStateChange)
	m.mu.Unlock()

	for _, cb := range callbacks {
		cb(old, StateMaintenance)
	}

	return nil
}

// IsMaintenanceExpired returns true if the node is in maintenance and the TTL
// has expired. Used by the leader's health monitor to enforce the max TTL.
func (m *Manager) IsMaintenanceExpired() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state != StateMaintenance {
		return false
	}
	return !m.maintenanceTTL.IsZero() && time.Now().After(m.maintenanceTTL)
}

// IsAvailable returns true if the node is in a state that can serve requests.
// Degraded counts: the node is up and answering, it just has not finished
// converging on the cluster. Treating it as unavailable would take a node that
// is serving perfectly good local traffic out of rotation for the duration of
// someone else's outage.
func (m *Manager) IsAvailable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateActive || m.state == StateDegraded
}

// IsInMaintenance returns true if the node is in maintenance mode.
func (m *Manager) IsInMaintenance() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateMaintenance
}

// Snapshot returns a point-in-time copy of the lifecycle state for
// embedding in metadata without holding the lock.
func (m *Manager) Snapshot() (state State, ttl time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state, m.maintenanceTTL
}
