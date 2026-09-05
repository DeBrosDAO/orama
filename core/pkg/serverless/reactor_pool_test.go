package serverless

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// mockModule is a minimal api.Module — the reactor pool only ever calls Close.
type mockModule struct {
	api.Module
	name   string
	closed *atomic.Bool
}

func (m *mockModule) Close(context.Context) error {
	m.closed.Store(true)
	return nil
}

// warmerSpy is a reactorWarmer that records every instance it creates so a test
// can assert warm counts + that evicted/invalidated instances are closed.
type warmerSpy struct {
	mu       sync.Mutex
	warmed   []*mockModule
	failNext atomic.Bool
}

func (w *warmerSpy) warm(_ context.Context, _ string, name string) (api.Module, error) {
	if w.failNext.Swap(false) {
		return nil, fmt.Errorf("synthetic warm failure")
	}
	m := &mockModule{name: name, closed: &atomic.Bool{}}
	w.mu.Lock()
	w.warmed = append(w.warmed, m)
	w.mu.Unlock()
	return m, nil
}

func (w *warmerSpy) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.warmed)
}

func (w *warmerSpy) closedCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, m := range w.warmed {
		if m.closed.Load() {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestReactorPool_warmsToTargetAndServes(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 2, 8, zap.NewNop())

	// First acquire on a cold pool misses but kicks off warming toward target.
	if inst, ok := p.Acquire("cidA"); ok || inst != nil {
		t.Fatal("cold acquire should miss (nil,false)")
	}
	waitFor(t, func() bool { return spy.count() >= 2 }, "pool warms to target=2")

	// Now a warm instance is served.
	inst, ok := p.Acquire("cidA")
	if !ok || inst == nil {
		t.Fatal("expected a warmed instance after warming")
	}
	// Taking one triggers a background refill back to target.
	waitFor(t, func() bool { return spy.count() >= 3 }, "pool refills after a take")
}

func TestReactorPool_oneShot_notReused(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 1, 8, zap.NewNop())
	waitForReady := func(cid string) api.Module {
		var got api.Module
		waitFor(t, func() bool {
			inst, ok := p.Acquire(cid)
			if ok {
				got = inst
				return true
			}
			return false
		}, "a warmed instance becomes available")
		return got
	}
	a := waitForReady("cid")
	b := waitForReady("cid")
	if a == b {
		t.Fatal("pool handed out the SAME instance twice — instances must be one-shot")
	}
}

func TestReactorPool_InstantiateSync(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 2, 8, zap.NewNop())
	inst, err := p.InstantiateSync(context.Background(), "cid")
	if err != nil || inst == nil {
		t.Fatalf("InstantiateSync: inst=%v err=%v", inst, err)
	}
}

func TestReactorPool_InstantiateSync_warmError(t *testing.T) {
	spy := &warmerSpy{}
	spy.failNext.Store(true)
	p := newReactorPool(spy.warm, 1, 8, zap.NewNop())
	if _, err := p.InstantiateSync(context.Background(), "cid"); err == nil {
		t.Fatal("expected error from a failing warm")
	}
}

func TestReactorPool_Invalidate_closesWarmed(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 2, 8, zap.NewNop())
	p.Acquire("cid") // triggers warming
	waitFor(t, func() bool { return spy.count() >= 2 }, "warmed 2")
	// Let the warmed instances land in the ready channel.
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		mp := p.pools["cid"]
		return mp != nil && len(mp.ready) == 2
	}, "2 instances ready")

	p.Invalidate("cid")
	waitFor(t, func() bool { return spy.closedCount() >= 2 }, "invalidate closes warmed instances")
}

func TestReactorPool_LRUEviction_closesEvicted(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 1, 2, zap.NewNop()) // maxModules=2

	// Populate 3 distinct module pools; the cap is 2, so the oldest is evicted.
	for _, cid := range []string{"cid1", "cid2"} {
		p.Acquire(cid)
		waitFor(t, func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()
			mp := p.pools[cid]
			return mp != nil && len(mp.ready) == 1
		}, cid+" ready")
	}
	// Acquiring a 3rd new module evicts the LRU (cid1) and closes its instance.
	p.Acquire("cid3")
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.pools["cid1"] == nil && len(p.pools) <= 2
	}, "cid1 evicted by LRU")
	waitFor(t, func() bool { return spy.closedCount() >= 1 }, "evicted instance closed")
}

// TestReactorPool_warmRacingInvalidate_noLeak is the regression for the
// code-review MEDIUM finding: a warm that completes AFTER its pool is torn down
// must close its instance, not leak it into a detached channel.
func TestReactorPool_warmRacingInvalidate_noLeak(t *testing.T) {
	release := make(chan struct{})
	var built atomic.Bool
	var leaked *mockModule
	blockingWarm := func(_ context.Context, _ string, name string) (api.Module, error) {
		<-release // block mid-warm until the test invalidates the pool
		m := &mockModule{name: name, closed: &atomic.Bool{}}
		leaked = m
		built.Store(true)
		return m, nil
	}
	p := newReactorPool(blockingWarm, 1, 8, zap.NewNop())

	p.Acquire("cid")    // kicks off a warm that now blocks on <-release
	p.Invalidate("cid") // tear the pool down while the warm is in flight
	close(release)      // let the warm complete

	waitFor(t, func() bool { return built.Load() }, "the in-flight warm completes")
	waitFor(t, func() bool { return leaked != nil && leaked.closed.Load() },
		"the post-teardown warmed instance is Closed, not leaked")
}

func TestReactorPool_Close_drainsAll(t *testing.T) {
	spy := &warmerSpy{}
	p := newReactorPool(spy.warm, 2, 8, zap.NewNop())
	p.Acquire("cidA")
	p.Acquire("cidB")
	waitFor(t, func() bool { return spy.count() >= 4 }, "warmed 4 across 2 pools")
	waitFor(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		ready := 0
		for _, mp := range p.pools {
			ready += len(mp.ready)
		}
		return ready == 4
	}, "4 ready total")
	p.Close()
	waitFor(t, func() bool { return spy.closedCount() >= 4 }, "Close drains+closes all warmed instances")
}
