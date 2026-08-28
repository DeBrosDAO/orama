package persistent

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// newProbeInstance builds an Instance wired to the fake module/exports so the
// locking contract can be exercised without a real wazero runtime.
func newProbeInstance(p *entryProbe, frameTimeout time.Duration) *Instance {
	mem := &fakeMemory{buf: make([]byte, 4096)}
	mod := &fakeModule{probe: p}
	return &Instance{
		clientID:     "probe-client",
		functionName: "rpc-router",
		namespace:    "test-ns",
		module:       mod,
		openFn:       &fakeFunction{probe: p},
		frameFn:      &fakeFunction{probe: p},
		closeFn:      &fakeFunction{probe: p},
		allocFn:      &fakeFunction{probe: p, result: 128},
		memory:       mem,
		inbound:      make(chan []byte, 16),
		logger:       zap.NewNop(),
		frameTimeout: frameTimeout,
		runDone:      make(chan struct{}),
	}
}

// TestInstance_callExport_neverConcurrent drives many frames and a concurrent
// Close, and fails if two goroutines were ever inside the module at once.
func TestInstance_callExport_neverConcurrent(t *testing.T) {
	p := &entryProbe{hold: 200 * time.Microsecond}
	i := newProbeInstance(p, 2*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		i.Run(ctx)
	}()

	// Feed frames while a second goroutine races the teardown — the exact
	// shape of the production crash (a client disconnecting mid-frame).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 200; n++ {
			if err := i.Submit([]byte("frame")); err != nil {
				return
			}
		}
	}()

	time.Sleep(5 * time.Millisecond)
	cancel()
	i.Close(context.Background(), CloseReasonClientDisconnect)
	wg.Wait()

	if got := p.maxInside.Load(); got > 1 {
		t.Fatalf("max concurrent module entries = %d; want 1 — concurrent entry into a wazero instance crashes the process", got)
	}
	if p.calls.Load() == 0 {
		t.Fatal("probe was never entered; the test did not exercise the module path")
	}
}

// TestInstance_Close_waitsForInflightFrame asserts the ordering half of the
// fix: ws_close must not run while a frame is still executing.
func TestInstance_Close_waitsForInflightFrame(t *testing.T) {
	p := &entryProbe{hold: 150 * time.Millisecond}
	i := newProbeInstance(p, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		i.Run(ctx)
		close(done)
	}()

	if err := i.Submit([]byte("slow-frame")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Let the frame get inside the module.
	time.Sleep(20 * time.Millisecond)

	cancel()
	i.Close(context.Background(), CloseReasonClientDisconnect)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after Close")
	}

	if got := p.maxInside.Load(); got > 1 {
		t.Fatalf("max concurrent module entries = %d; want 1", got)
	}
}

// Close must not block forever when Run was never started — the ws_open
// rejection path closes an instance whose frame loop never launches.
func TestInstance_Close_withoutRun_returnsPromptly(t *testing.T) {
	p := &entryProbe{}
	i := newProbeInstance(p, 30*time.Second)

	done := make(chan struct{})
	go func() {
		i.Close(context.Background(), CloseReasonRejected)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked waiting on a frame loop that was never started")
	}
}

// Close is idempotent and safe from several goroutines at once.
func TestInstance_Close_idempotentUnderConcurrency(t *testing.T) {
	p := &entryProbe{}
	i := newProbeInstance(p, time.Second)

	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			i.Close(context.Background(), CloseReasonServerShutdown)
		}()
	}
	wg.Wait()

	if got := p.maxInside.Load(); got > 1 {
		t.Fatalf("max concurrent module entries = %d; want 1", got)
	}
}
