package lifecycle

import (
	"fmt"
	"sync"
	"time"
)

// State represents a node's lifecycle state.
type State string

const (
	StateJoining     State = "joining"
	StateActive      State = "active"
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
	StateJoining:     {StateActive: true},
	StateActive:      {StateDraining: true, StateMaintenance: true},
	StateDraining:    {StateMaintenance: true},
	StateMaintenance: {StateActive: true},
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
	m.mu.Lock()
	old := m.state

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

	// Allow both active→maintenance and draining→maintenance
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
func (m *Manager) IsAvailable() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state == StateActive
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
