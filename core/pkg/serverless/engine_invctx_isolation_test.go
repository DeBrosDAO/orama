package serverless

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

// TestEngine_Execute_concurrent_invCtx_isolation is the regression
// guard for bugboard #348's stateless singleton race.
//
// Pre-fix, Engine.Execute set the HostFunctions singleton h.invCtx via
// contextSetter on entry and cleared it via defer on exit. Two
// concurrent stateless invocations racing on that field produced two
// failure modes:
//
//  1. Cross-tenant leak: G2's setter overwrites G1's, and G1's
//     subsequent host-fn read returns G2's namespace.
//  2. Nil-namespace error: G1's clearer fires before G2's WASM has
//     called the host fn, so G2 reads a nil singleton and the host fn
//     returns the AnChat-observed "no namespace in invocation context"
//     error.
//
// Post-fix, Engine.Execute attaches invCtx to the execCtx via
// WithInvocationContext. wazero propagates that ctx through to host-fn
// callbacks. The hostfunctions resolver (currentInvocationContext)
// prefers ctx-attached over the singleton, so each invocation now
// sees its OWN identity regardless of what the singleton holds.
//
// This test fires 32 concurrent invocations, each with a distinct
// namespace, and asserts the host fn that handles their log_info call
// receives a ctx carrying THAT goroutine's invCtx. A single mismatch
// (zero detections) means the race is back. Run with -race for
// stronger signal.
func TestEngine_Execute_concurrent_invCtx_isolation(t *testing.T) {
	logger := zap.NewNop()
	registry := NewMockRegistry()

	// Custom HostServices that captures the ctx-attached invCtx from
	// every LogInfo call. The WASM module below calls log_info from
	// _start with ptr=0 size=0; that's an empty-string log payload but
	// importantly the host-fn invocation DOES fire, giving us the ctx
	// to inspect.
	cap := &capturingHostServices{
		MockHostServices: NewMockHostServices(),
	}

	engine, err := NewEngine(nil, registry, cap, logger)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(context.Background())

	fnDef := &FunctionDefinition{
		Name:           "push-fanout",
		Namespace:      "anchat-test",
		MemoryLimitMB:  64,
		TimeoutSeconds: 5,
	}
	if _, err := registry.Register(context.Background(), fnDef, wasmCallsLogInfo); err != nil {
		t.Fatalf("Register: %v", err)
	}
	fn, err := registry.Get(context.Background(), "anchat-test", "push-fanout", 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	const goroutines = 32

	var (
		wg            sync.WaitGroup
		mismatches    int64
		nilCaptures   int64
		execErrors    int64
		firstExecErr  error
		firstMismatch string
		errMu         sync.Mutex
	)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()

			// Distinct per-goroutine identity. Namespace is the field
			// the AnChat path actually reads (PushSendV2 fails when
			// invCtx.Namespace == "").
			myNS := fmt.Sprintf("ns-tenant-%d", gid)
			myReq := fmt.Sprintf("req-%d", gid)
			invCtx := &InvocationContext{
				Namespace:    myNS,
				FunctionName: fn.Name,
				RequestID:    myReq,
				TriggerType:  TriggerTypePubSub,
			}

			// Reset the per-goroutine capture slot before invoke.
			cap.clearCapture(myReq)

			if _, err := engine.Execute(context.Background(), fn, []byte("x"), invCtx); err != nil {
				atomic.AddInt64(&execErrors, 1)
				errMu.Lock()
				if firstExecErr == nil {
					firstExecErr = err
				}
				errMu.Unlock()
				return
			}

			got := cap.captureFor(myReq)
			if got == nil {
				atomic.AddInt64(&nilCaptures, 1)
				return
			}
			if got.Namespace != myNS {
				atomic.AddInt64(&mismatches, 1)
				errMu.Lock()
				if firstMismatch == "" {
					firstMismatch = fmt.Sprintf("goroutine %d: ctx invCtx.Namespace = %q; want %q",
						gid, got.Namespace, myNS)
				}
				errMu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	if execErrors > 0 {
		t.Fatalf("%d/%d invocations errored at Execute (first: %v) — the host fn isn't even getting called",
			execErrors, goroutines, firstExecErr)
	}
	if nilCaptures > 0 {
		t.Fatalf("%d/%d host-fn calls saw nil invCtx on ctx — the fix isn't attaching invCtx to execCtx (bugboard #348 stateless race)",
			nilCaptures, goroutines)
	}
	if mismatches > 0 {
		t.Fatalf("%d/%d cross-tenant leaks detected. example: %s — invCtx is bleeding across concurrent stateless invocations (bugboard #348)",
			mismatches, goroutines, firstMismatch)
	}
}

// capturingHostServices wraps MockHostServices and records the
// ctx-attached InvocationContext from each LogInfo call, keyed by the
// invCtx's RequestID. That key lets a goroutine recover ITS OWN
// capture without coordinating with other goroutines.
type capturingHostServices struct {
	*MockHostServices

	capMu    sync.Mutex
	captures map[string]*InvocationContext // requestID → invCtx seen by host fn
}

func (c *capturingHostServices) LogInfo(ctx context.Context, message string) {
	got := InvocationContextFromCtx(ctx)
	c.capMu.Lock()
	if c.captures == nil {
		c.captures = make(map[string]*InvocationContext)
	}
	if got != nil {
		c.captures[got.RequestID] = got
	} else {
		// Record nil under a sentinel so the test can count nil
		// captures. RequestID isn't known here because invCtx is nil
		// — fall through; the test detects nil via captureFor returning
		// nil for the goroutine's RequestID.
	}
	c.capMu.Unlock()

	// Delegate to base for any other recording (log slice, etc.).
	c.MockHostServices.LogInfo(ctx, message)
}

func (c *capturingHostServices) captureFor(requestID string) *InvocationContext {
	c.capMu.Lock()
	defer c.capMu.Unlock()
	return c.captures[requestID]
}

func (c *capturingHostServices) clearCapture(requestID string) {
	c.capMu.Lock()
	defer c.capMu.Unlock()
	if c.captures != nil {
		delete(c.captures, requestID)
	}
}

// wasmCallsLogInfo is a hand-assembled WASM binary equivalent to:
//
//	(module
//	  (type $log   (func (param i32 i32)))   ; type 0
//	  (type $start (func))                    ; type 1
//	  (import "env" "log_info" (func $log_info (type 0)))
//	  (memory (export "memory") 1)
//	  (func $_start (type 1)
//	    i32.const 0
//	    i32.const 0
//	    call $log_info)
//	  (export "_start" (func $_start)))
//
// log_info(ptr=0, size=0) triggers the host-fn callback with an empty
// payload. The host fn reads memory[0:0] (zero-length read, succeeds)
// and dispatches LogInfo(ctx, ""). We don't care about the payload —
// we care that the host fn fires so the test can inspect the ctx
// that reached it.
//
// Reference: https://webassembly.github.io/spec/core/binary/modules.html
var wasmCallsLogInfo = []byte{
	// Magic + version
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// Type section (id=1) — body=9 bytes
	0x01,                   // section id
	0x09,                   // section size = 9
	0x02,                   // 2 types
	0x60, 0x02, 0x7f, 0x7f, // type 0: func (i32, i32) -> ...
	0x00,       // type 0 results = 0
	0x60, 0x00, // type 1: func () -> ...
	0x00, // type 1 results = 0

	// Import section (id=2) — body=16 bytes
	0x02,                   // section id
	0x10,                   // section size = 16
	0x01,                   // 1 import
	0x03, 0x65, 0x6e, 0x76, // module = "env" (3 bytes)
	0x08, 0x6c, 0x6f, 0x67, 0x5f, 0x69, 0x6e, 0x66, 0x6f, // fn = "log_info" (8 bytes)
	0x00, 0x00, // kind=func, type idx=0

	// Function section (id=3)
	0x03,       // section id
	0x02,       // section size = 2
	0x01, 0x01, // 1 function, type idx=1

	// Memory section (id=5)
	0x05,             // section id
	0x03,             // section size = 3
	0x01, 0x00, 0x01, // 1 memory: limits flag=0 (no max), min=1 page

	// Export section (id=7)
	0x07,                                     // section id
	0x13,                                     // section size = 19
	0x02,                                     // 2 exports
	0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, // "memory" (6 bytes)
	0x02, 0x00, // kind=memory, idx=0
	0x06, 0x5f, 0x73, 0x74, 0x61, 0x72, 0x74, // "_start" (6 bytes)
	0x00, 0x01, // kind=func, idx=1 (import is func idx 0)

	// Code section (id=10)
	0x0a,       // section id
	0x0a,       // section size = 10
	0x01,       // 1 function body
	0x08,       // body size = 8
	0x00,       // 0 local groups
	0x41, 0x00, // i32.const 0
	0x41, 0x00, // i32.const 0
	0x10, 0x00, // call 0 (calls log_info import)
	0x0b, // end
}
