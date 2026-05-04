package persistent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Manager tracks live persistent instances per gateway and enforces a
// global capacity cap. Connections beyond the cap are rejected with
// HTTP 503; we never evict an existing connection to make room — that
// would break user expectations on a long-lived chat session.
type Manager struct {
	capacity    int32
	activeCount atomic.Int32

	mu        sync.RWMutex
	instances map[string]*Instance // clientID -> instance

	logger *zap.Logger
}

// NewManager constructs a Manager with the given concurrency cap. capacity
// <= 0 falls back to 5000.
func NewManager(capacity int, logger *zap.Logger) *Manager {
	if capacity <= 0 {
		capacity = 5000
	}
	return &Manager{
		capacity:  int32(capacity),
		instances: make(map[string]*Instance),
		logger:    logger,
	}
}

// Acquire reserves a capacity slot. Returns false if at capacity. Caller
// MUST call Release when the connection ends, or the slot leaks.
func (m *Manager) Acquire() bool {
	if m.activeCount.Load() >= m.capacity {
		return false
	}
	m.activeCount.Add(1)
	return true
}

// Release frees a capacity slot. Safe to call even if the corresponding
// Acquire returned false (no-op).
func (m *Manager) Release() {
	if c := m.activeCount.Load(); c > 0 {
		m.activeCount.Add(-1)
	}
}

// Register stores the instance under its client ID for later lookup
// (e.g. by ws_send hostfunc resolving its own client). Replaces any
// existing registration for the same ID.
func (m *Manager) Register(inst *Instance) {
	m.mu.Lock()
	m.instances[inst.ClientID()] = inst
	m.mu.Unlock()
}

// Unregister removes the instance. Does NOT call Close — the caller is
// responsible for that, since Close needs a context.
func (m *Manager) Unregister(clientID string) {
	m.mu.Lock()
	delete(m.instances, clientID)
	m.mu.Unlock()
}

// Lookup returns the instance for a client ID, or false if absent.
func (m *Manager) Lookup(clientID string) (*Instance, bool) {
	m.mu.RLock()
	inst, ok := m.instances[clientID]
	m.mu.RUnlock()
	return inst, ok
}

// ActiveCount returns the current number of registered persistent instances.
// Useful for metrics; exact at the moment of call but may be stale immediately.
func (m *Manager) ActiveCount() int {
	return int(m.activeCount.Load())
}

// ShutdownAll calls ws_close on every active instance, bounded by `total`.
// Each instance gets at most `total / N` of the budget — designed so a few
// slow handlers can't starve the gateway shutdown.
//
// Returns when all instances have closed or the budget is exhausted.
func (m *Manager) ShutdownAll(total time.Duration) {
	m.mu.Lock()
	snapshot := make([]*Instance, 0, len(m.instances))
	for _, inst := range m.instances {
		snapshot = append(snapshot, inst)
	}
	m.mu.Unlock()

	if len(snapshot) == 0 {
		return
	}

	per := total / time.Duration(len(snapshot))
	if per < 100*time.Millisecond {
		per = 100 * time.Millisecond
	}

	var wg sync.WaitGroup
	for _, inst := range snapshot {
		wg.Add(1)
		go func(inst *Instance) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), per)
			defer cancel()
			inst.Close(ctx, CloseReasonServerShutdown)
		}(inst)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(total):
		m.logger.Warn("persistent.Manager.ShutdownAll timed out",
			zap.Int("active_at_shutdown", len(snapshot)),
			zap.Duration("budget", total))
	}
}
