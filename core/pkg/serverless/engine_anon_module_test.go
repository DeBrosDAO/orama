package serverless

import (
	"context"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// minimalWASM is the same nop-only WASM from TestEngine_Execute —
// exports _start, returns immediately. Sufficient for proving that
// ExecuteModule's instantiation contract works under repeated /
// concurrent calls without name-collision (bug #221).
var minimalWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0a, 0x01, 0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, 0x00, 0x00,
	0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
}

// Bug #221 regression: invocations of the SAME function must succeed
// repeatedly. Before the fix, the second invocation hit
// `module[<fn>] has already been instantiated` because the executor
// passed the function name to wazero's WithName which globally
// registered it.
func TestEngine_RepeatedInvocations_SameFunction_NoCollision(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(context.Background())

	fnDef := &FunctionDefinition{
		Name:           "username-check",
		Namespace:      "anchat-test",
		MemoryLimitMB:  64,
		TimeoutSeconds: 5,
	}
	if _, err := registry.Register(context.Background(), fnDef, minimalWASM); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn, err := registry.Get(context.Background(), "anchat-test", "username-check", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Five sequential invocations — each must succeed.
	for i := 0; i < 5; i++ {
		if _, err := engine.Execute(context.Background(), fn, []byte("x"), nil); err != nil {
			t.Fatalf("invocation %d failed: %v", i, err)
		}
	}
}

// Bug #221 regression: two CONCURRENT invocations of the same function
// must both succeed. Before the fix this was THE most reliable repro —
// the second goroutine's InstantiateModule lost the race for the global
// name slot.
func TestEngine_ConcurrentInvocations_SameFunction_NoCollision(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(context.Background())

	fnDef := &FunctionDefinition{
		Name:           "user-create",
		Namespace:      "anchat-test",
		MemoryLimitMB:  64,
		TimeoutSeconds: 5,
	}
	if _, err := registry.Register(context.Background(), fnDef, minimalWASM); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn, err := registry.Get(context.Background(), "anchat-test", "user-create", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	const goroutines = 16
	const iterations = 10

	var (
		wg          sync.WaitGroup
		errMu       sync.Mutex
		firstErr    error
		errPayloads []string
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, err := engine.Execute(context.Background(), fn, []byte("x"), nil); err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errPayloads = append(errPayloads, err.Error())
					errMu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		// Surface ALL distinct error messages — easier to debug than the
		// first one alone if the runtime starts failing weirdly.
		t.Fatalf("concurrent invocations hit %d errors. first: %v\nall: %v",
			len(errPayloads), firstErr, errPayloads)
	}
}

// Bug #221 regression: re-deploy of the same function (new version, new
// WASM CID) followed by an invocation must succeed. The old compiled
// module's cache slot must not block the new instance from running.
func TestEngine_ReDeploy_ThenInvoke_NoCollision(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()
	hostServices := NewMockHostServices()
	engine, err := NewEngine(nil, registry, hostServices, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(context.Background())

	fnDef := &FunctionDefinition{
		Name:           "wallet-link",
		Namespace:      "anchat-test",
		MemoryLimitMB:  64,
		TimeoutSeconds: 5,
	}

	// First deploy + invoke.
	if _, err := registry.Register(context.Background(), fnDef, minimalWASM); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	fn1, _ := registry.Get(context.Background(), "anchat-test", "wallet-link", 0)
	if _, err := engine.Execute(context.Background(), fn1, []byte("x"), nil); err != nil {
		t.Fatalf("first invoke: %v", err)
	}

	// Re-deploy: same name, new bytes (still valid). MockRegistry overwrites
	// the function record; the engine sees a fresh wasmCID.
	if _, err := registry.Register(context.Background(), fnDef, minimalWASM); err != nil {
		t.Fatalf("re-deploy: %v", err)
	}
	fn2, _ := registry.Get(context.Background(), "anchat-test", "wallet-link", 0)

	// Multiple invocations after re-deploy — exactly the AnChat repro.
	for i := 0; i < 3; i++ {
		_, err := engine.Execute(context.Background(), fn2, []byte("x"), nil)
		if err != nil {
			if strings.Contains(err.Error(), "already been instantiated") {
				t.Fatalf("bug #221 regression: %v", err)
			}
			t.Fatalf("post-redeploy invoke %d failed: %v", i, err)
		}
	}
}
