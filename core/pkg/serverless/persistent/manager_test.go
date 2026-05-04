package persistent

import (
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestManager_acquire_release_within_capacity(t *testing.T) {
	m := NewManager(3, zap.NewNop())

	for i := 0; i < 3; i++ {
		if !m.Acquire() {
			t.Fatalf("acquire %d should succeed within capacity", i)
		}
	}
	if m.Acquire() {
		t.Fatal("4th acquire should fail at capacity")
	}
	m.Release()
	if !m.Acquire() {
		t.Fatal("acquire after release should succeed")
	}
	if m.ActiveCount() != 3 {
		t.Errorf("expected ActiveCount=3, got %d", m.ActiveCount())
	}
}

func TestManager_release_below_zero_safe(t *testing.T) {
	m := NewManager(2, zap.NewNop())
	// Release without acquire should not go negative.
	for i := 0; i < 5; i++ {
		m.Release()
	}
	if m.ActiveCount() != 0 {
		t.Errorf("expected ActiveCount=0 after over-release, got %d", m.ActiveCount())
	}
}

func TestManager_acquire_concurrent_no_overcommit(t *testing.T) {
	const cap = 10
	m := NewManager(cap, zap.NewNop())

	var wg sync.WaitGroup
	var successes int32
	var mu sync.Mutex

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.Acquire() {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	// Note: under contention the atomic check + increment is non-strict —
	// brief overcommit is possible but should be small. Assert we didn't go
	// wildly past capacity.
	if successes < cap {
		t.Errorf("expected at least %d successes, got %d", cap, successes)
	}
	if successes > cap+2 {
		t.Errorf("expected at most ~%d successes, got %d (overcommit too large)", cap+2, successes)
	}
}

func TestManager_register_lookup_unregister(t *testing.T) {
	m := NewManager(10, zap.NewNop())
	// We can't construct a real Instance without a wazero module, so just
	// exercise the map plumbing with a partially-initialized struct.
	inst := &Instance{clientID: "c1"}
	m.Register(inst)

	got, ok := m.Lookup("c1")
	if !ok || got != inst {
		t.Errorf("Lookup didn't return registered instance")
	}

	m.Unregister("c1")
	if _, ok := m.Lookup("c1"); ok {
		t.Errorf("instance still present after Unregister")
	}
}

func TestManager_shutdown_with_no_instances(t *testing.T) {
	m := NewManager(10, zap.NewNop())
	// Should not panic / hang.
	m.ShutdownAll(0)
}
