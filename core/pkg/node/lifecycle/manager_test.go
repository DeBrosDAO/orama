package lifecycle

import (
	"sync"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m.State() != StateJoining {
		t.Fatalf("expected initial state %q, got %q", StateJoining, m.State())
	}
	if m.IsAvailable() {
		t.Fatal("joining node should not be available")
	}
	if m.IsInMaintenance() {
		t.Fatal("joining node should not be in maintenance")
	}
}

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		to      State
		wantErr bool
	}{
		{"joining→active", StateJoining, StateActive, false},
		{"active→draining", StateActive, StateDraining, false},
		{"draining→maintenance", StateDraining, StateMaintenance, false},
		{"active→maintenance", StateActive, StateMaintenance, false},
		{"maintenance→active", StateMaintenance, StateActive, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{state: tt.from, enterTime: time.Now()}
			err := m.TransitionTo(tt.to)
			if (err != nil) != tt.wantErr {
				t.Fatalf("TransitionTo(%q): err=%v, wantErr=%v", tt.to, err, tt.wantErr)
			}
			if err == nil && m.State() != tt.to {
				t.Fatalf("expected state %q, got %q", tt.to, m.State())
			}
		})
	}
}

func TestInvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from State
		to   State
	}{
		{"joining→draining", StateJoining, StateDraining},
		{"joining→maintenance", StateJoining, StateMaintenance},
		{"joining→joining", StateJoining, StateJoining},
		{"active→active", StateActive, StateActive},
		{"active→joining", StateActive, StateJoining},
		{"draining→active", StateDraining, StateActive},
		{"draining→joining", StateDraining, StateJoining},
		{"maintenance→draining", StateMaintenance, StateDraining},
		{"maintenance→joining", StateMaintenance, StateJoining},
		{"maintenance→maintenance", StateMaintenance, StateMaintenance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{state: tt.from, enterTime: time.Now()}
			err := m.TransitionTo(tt.to)
			if err == nil {
				t.Fatalf("expected error for transition %s → %s", tt.from, tt.to)
			}
		})
	}
}

func TestEnterMaintenance(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)

	err := m.EnterMaintenance(5 * time.Minute)
	if err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	if !m.IsInMaintenance() {
		t.Fatal("expected maintenance state")
	}

	ttl := m.MaintenanceTTL()
	if ttl.IsZero() {
		t.Fatal("expected non-zero maintenance TTL")
	}

	// TTL should be roughly 5 minutes from now
	remaining := time.Until(ttl)
	if remaining < 4*time.Minute || remaining > 6*time.Minute {
		t.Fatalf("expected TTL ~5min from now, got %v", remaining)
	}
}

func TestEnterMaintenanceTTLCapped(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)

	// Request 1 hour, should be capped at MaxMaintenanceTTL
	err := m.EnterMaintenance(1 * time.Hour)
	if err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	ttl := m.MaintenanceTTL()
	remaining := time.Until(ttl)
	if remaining > MaxMaintenanceTTL+time.Second {
		t.Fatalf("TTL should be capped at %v, got %v remaining", MaxMaintenanceTTL, remaining)
	}
}

func TestEnterMaintenanceZeroTTL(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)

	// Zero TTL should default to MaxMaintenanceTTL
	err := m.EnterMaintenance(0)
	if err != nil {
		t.Fatalf("EnterMaintenance: %v", err)
	}

	ttl := m.MaintenanceTTL()
	remaining := time.Until(ttl)
	if remaining < MaxMaintenanceTTL-time.Second {
		t.Fatalf("zero TTL should default to MaxMaintenanceTTL, got %v remaining", remaining)
	}
}

func TestMaintenanceTTLClearedOnExit(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)
	_ = m.EnterMaintenance(5 * time.Minute)

	if m.MaintenanceTTL().IsZero() {
		t.Fatal("expected non-zero TTL in maintenance")
	}

	_ = m.TransitionTo(StateActive)

	if !m.MaintenanceTTL().IsZero() {
		t.Fatal("expected zero TTL after leaving maintenance")
	}
}

func TestIsMaintenanceExpired(t *testing.T) {
	m := &Manager{
		state:          StateMaintenance,
		maintenanceTTL: time.Now().Add(-1 * time.Minute), // expired 1 minute ago
		enterTime:      time.Now().Add(-20 * time.Minute),
	}

	if !m.IsMaintenanceExpired() {
		t.Fatal("expected maintenance to be expired")
	}

	// Not expired
	m.maintenanceTTL = time.Now().Add(5 * time.Minute)
	if m.IsMaintenanceExpired() {
		t.Fatal("expected maintenance to not be expired")
	}

	// Not in maintenance
	m.state = StateActive
	if m.IsMaintenanceExpired() {
		t.Fatal("expected non-maintenance state to not report expired")
	}
}

func TestStateChangeCallback(t *testing.T) {
	m := NewManager()

	var callbackOld, callbackNew State
	called := false
	m.OnStateChange(func(old, new State) {
		callbackOld = old
		callbackNew = new
		called = true
	})

	_ = m.TransitionTo(StateActive)

	if !called {
		t.Fatal("callback was not called")
	}
	if callbackOld != StateJoining || callbackNew != StateActive {
		t.Fatalf("callback got old=%q new=%q, want old=%q new=%q",
			callbackOld, callbackNew, StateJoining, StateActive)
	}
}

func TestMultipleCallbacks(t *testing.T) {
	m := NewManager()

	count := 0
	m.OnStateChange(func(_, _ State) { count++ })
	m.OnStateChange(func(_, _ State) { count++ })

	_ = m.TransitionTo(StateActive)

	if count != 2 {
		t.Fatalf("expected 2 callbacks, got %d", count)
	}
}

func TestSnapshot(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)
	_ = m.EnterMaintenance(10 * time.Minute)

	state, ttl := m.Snapshot()
	if state != StateMaintenance {
		t.Fatalf("expected maintenance, got %q", state)
	}
	if ttl.IsZero() {
		t.Fatal("expected non-zero TTL in snapshot")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := NewManager()
	_ = m.TransitionTo(StateActive)

	var wg sync.WaitGroup
	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.State()
			_ = m.IsAvailable()
			_ = m.IsInMaintenance()
			_ = m.IsMaintenanceExpired()
			_, _ = m.Snapshot()
		}()
	}

	// Concurrent maintenance enter/exit cycles
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.EnterMaintenance(1 * time.Minute)
			_ = m.TransitionTo(StateActive)
		}()
	}

	wg.Wait()
}

func TestStateEnteredAt(t *testing.T) {
	before := time.Now()
	m := NewManager()
	after := time.Now()

	entered := m.StateEnteredAt()
	if entered.Before(before) || entered.After(after) {
		t.Fatalf("StateEnteredAt %v not between %v and %v", entered, before, after)
	}

	time.Sleep(10 * time.Millisecond)
	_ = m.TransitionTo(StateActive)

	newEntered := m.StateEnteredAt()
	if !newEntered.After(entered) {
		t.Fatal("expected StateEnteredAt to update after transition")
	}
}

func TestEnterMaintenanceFromInvalidState(t *testing.T) {
	m := NewManager() // joining state
	err := m.EnterMaintenance(5 * time.Minute)
	if err == nil {
		t.Fatal("expected error entering maintenance from joining state")
	}
}

func TestFullLifecycle(t *testing.T) {
	m := NewManager()

	// joining → active
	if err := m.TransitionTo(StateActive); err != nil {
		t.Fatalf("joining→active: %v", err)
	}
	if !m.IsAvailable() {
		t.Fatal("active node should be available")
	}

	// active → draining
	if err := m.TransitionTo(StateDraining); err != nil {
		t.Fatalf("active→draining: %v", err)
	}
	if m.IsAvailable() {
		t.Fatal("draining node should not be available")
	}

	// draining → maintenance
	if err := m.EnterMaintenance(10 * time.Minute); err != nil {
		t.Fatalf("draining→maintenance: %v", err)
	}
	if !m.IsInMaintenance() {
		t.Fatal("should be in maintenance")
	}

	// maintenance → active
	if err := m.TransitionTo(StateActive); err != nil {
		t.Fatalf("maintenance→active: %v", err)
	}
	if !m.IsAvailable() {
		t.Fatal("should be available after maintenance")
	}
}
