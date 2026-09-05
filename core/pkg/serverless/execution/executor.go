package execution

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// InstantiateTiming captures how long the per-invocation wazero
// InstantiateModule call took (running TinyGo _start / package init). It rides
// the ctx so the engine's slow-invoke diagnostic can split the execute phase
// into cold-start (instantiate) vs handler work (run) — the distinction that
// pins the bugboard #27 cold-start floor. Nil collector = not measured.
type InstantiateTiming struct {
	InstantiateNs int64
}

type instantiateTimingKey struct{}

// WithInstantiateTiming returns a ctx carrying a fresh InstantiateTiming that
// ExecuteModule will fill in. The caller reads it back after ExecuteModule.
func WithInstantiateTiming(ctx context.Context) (context.Context, *InstantiateTiming) {
	t := &InstantiateTiming{}
	return context.WithValue(ctx, instantiateTimingKey{}, t), t
}

func instantiateTimingFrom(ctx context.Context) *InstantiateTiming {
	t, _ := ctx.Value(instantiateTimingKey{}).(*InstantiateTiming)
	return t
}

type namespaceKey struct{}

// WithNamespace tags an execution ctx so the limiter can cap one tenant
// without letting it fill the process-wide semaphore (bugboard #85).
func WithNamespace(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, namespaceKey{}, ns)
}

func namespaceFrom(ctx context.Context) string {
	ns, _ := ctx.Value(namespaceKey{}).(string)
	return ns
}

// Executor handles WASM module execution.
type Executor struct {
	runtime wazero.Runtime
	logger  *zap.Logger
	sem     chan struct{} // process-wide limiter
	perNS   map[string]chan struct{}
	perNSN  int
	mu      sync.Mutex
}

// NewExecutor creates a new Executor.
// maxConcurrent limits simultaneous module instantiations (0 = unlimited).
func NewExecutor(runtime wazero.Runtime, logger *zap.Logger, maxConcurrent int) *Executor {
	var sem chan struct{}
	perNSN := 0
	if maxConcurrent > 0 {
		sem = make(chan struct{}, maxConcurrent)
		perNSN = maxConcurrent / 2
		if perNSN < 1 {
			perNSN = 1
		}
	}
	return &Executor{
		runtime: runtime,
		logger:  logger,
		sem:     sem,
		perNS:   make(map[string]chan struct{}),
		perNSN:  perNSN,
	}
}

func (e *Executor) nsSlot(ns string) chan struct{} {
	if e.perNSN <= 0 || ns == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := e.perNS[ns]
	if ch == nil {
		ch = make(chan struct{}, e.perNSN)
		e.perNS[ns] = ch
	}
	return ch
}

// ExecuteModule instantiates and runs a WASM module with the given input.
// The invocation's identity rides ctx; there is nothing to set on the host
// services, which hold no per-invocation state.
//
// Bug #221 fix: each invocation gets an ANONYMOUS module instance (no name
// in the wazero runtime registry). Previously we used the function name
// as the module name, which made wazero refuse the second instantiation
// with "module[<fn>] has already been instantiated" any time:
//
//   - Two invocations of the same function ran concurrently
//   - A re-deploy happened while a previous invocation was still mid-flight
//   - An instance.Close() raced with the next instantiation
//
// Anonymous instances eliminate the collision class entirely. The
// function name is still surfaced via WithArgs(moduleName) so WASI
// `argv[0]` shows it inside the function (matches the prior behavior
// that user code may depend on).
func (e *Executor) ExecuteModule(ctx context.Context, compiled wazero.CompiledModule, moduleName string, input []byte) ([]byte, error) {
	// Set invocation context for host functions

	// Create buffers for stdin/stdout (WASI uses these for I/O)
	stdin := bytes.NewReader(input)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// Create module configuration with WASI stdio.
	//
	// WithName("") = anonymous instance, NOT registered under the runtime's
	// global module name table. argv[0] still carries the function name for
	// WASI-consuming code that reads os.Args[0] (TinyGo's runtime + apps
	// that log it). See doc comment above for the bug-#221 rationale.
	moduleConfig := wazero.NewModuleConfig().
		WithName("").
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithArgs(moduleName). // argv[0] is the program name
		// Bugboard #27 — wazero defaults to fake/sentinel clocks. Without
		// these opt-ins, TinyGo's time.Now() returns ~2022-01-01T00:00:00.001Z
		// frozen on every read, silently poisoning timestamps in every
		// invocation that uses time.Now() (receipts, audit rows, cursor cmp).
		// Same fix applied at engine.go for the persistent-WS path.
		WithSysWalltime().
		WithSysNanotime().
		// Bugboard #120 — same class as #27. Without WithRandSource, wazero
		// uses a deterministic zero-seed RNG, so TinyGo's crypto/rand.Read
		// returns IDENTICAL bytes on every fresh instance (and every
		// invocation is a fresh instance). That makes any unguessable ID /
		// code / nonce / token constant. Wire in the host CSPRNG so
		// crypto/rand (and auto-seeded math/rand) work. Same fix at
		// engine.go for the persistent-WS path.
		WithRandSource(cryptorand.Reader)

	if e.sem != nil {
		select {
		case e.sem <- struct{}{}:
			defer func() { <-e.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if ns := namespaceFrom(ctx); ns != "" {
		if slot := e.nsSlot(ns); slot != nil {
			select {
			case slot <- struct{}{}:
				defer func() { <-slot }()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	// Instantiate and run the module (WASI _start will be called automatically).
	// Time the instantiate so the engine can attribute cold-start vs handler
	// work (bugboard #27 cold-start floor); no-op when no collector is attached.
	instStart := time.Now()
	instance, err := e.runtime.InstantiateModule(ctx, compiled, moduleConfig)
	if t := instantiateTimingFrom(ctx); t != nil {
		t.InstantiateNs = time.Since(instStart).Nanoseconds()
	}
	if err != nil {
		// Check if stderr has any output
		if stderr.Len() > 0 {
			e.logger.Warn("WASM stderr output", zap.String("stderr", stderr.String()))
		}
		return nil, fmt.Errorf("failed to instantiate module: %w", err)
	}
	defer instance.Close(ctx)

	// For WASI modules, the output is already in stdout buffer
	// The _start function was called during instantiation
	output := stdout.Bytes()

	// Log stderr if any
	if stderr.Len() > 0 {
		e.logger.Debug("WASM stderr", zap.String("stderr", stderr.String()))
	}

	return output, nil
}

// CallHandleFunction calls the main 'handle' export in the WASM module.
// This is an alternative execution path for modules that export a 'handle' function.
func (e *Executor) CallHandleFunction(ctx context.Context, instance api.Module, input []byte) ([]byte, error) {
	// Get the 'handle' function export
	handleFn := instance.ExportedFunction("handle")
	if handleFn == nil {
		return nil, fmt.Errorf("WASM module does not export 'handle' function")
	}

	// Get memory export
	memory := instance.ExportedMemory("memory")
	if memory == nil {
		return nil, fmt.Errorf("WASM module does not export 'memory'")
	}

	// Get malloc/free exports for memory management
	mallocFn := instance.ExportedFunction("malloc")
	freeFn := instance.ExportedFunction("free")

	var inputPtr uint32
	var inputLen = uint32(len(input))

	if mallocFn != nil && len(input) > 0 {
		// Allocate memory for input
		results, err := mallocFn.Call(ctx, uint64(inputLen))
		if err != nil {
			return nil, fmt.Errorf("malloc failed: %w", err)
		}
		inputPtr = uint32(results[0])

		// Write input to memory
		if !memory.Write(inputPtr, input) {
			return nil, fmt.Errorf("failed to write input to WASM memory")
		}

		// Defer free if available
		if freeFn != nil {
			defer func() {
				_, _ = freeFn.Call(ctx, uint64(inputPtr))
			}()
		}
	}

	// Call handle(input_ptr, input_len)
	// Returns: output_ptr (packed with length in upper 32 bits)
	results, err := handleFn.Call(ctx, uint64(inputPtr), uint64(inputLen))
	if err != nil {
		return nil, fmt.Errorf("handle function error: %w", err)
	}

	if len(results) == 0 {
		return nil, nil // No output
	}

	// Parse result - assume format: lower 32 bits = ptr, upper 32 bits = len
	result := results[0]
	outputPtr := uint32(result & 0xFFFFFFFF)
	outputLen := uint32(result >> 32)

	if outputLen == 0 {
		return nil, nil
	}

	// Read output from memory
	output, ok := memory.Read(outputPtr, outputLen)
	if !ok {
		return nil, fmt.Errorf("failed to read output from WASM memory")
	}

	// Make a copy (memory will be freed)
	outputCopy := make([]byte, len(output))
	copy(outputCopy, output)

	return outputCopy, nil
}

// WriteToGuest allocates memory in the guest WASM module and writes data to it.
// Returns a packed uint64 with ptr in upper 32 bits and length in lower 32 bits.
func (e *Executor) WriteToGuest(ctx context.Context, mod api.Module, data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	// Try to find a non-conflicting allocator first, fallback to malloc
	malloc := mod.ExportedFunction("orama_alloc")
	if malloc == nil {
		malloc = mod.ExportedFunction("malloc")
	}

	if malloc == nil {
		e.logger.Warn("WASM module missing malloc/orama_alloc export, cannot return string/bytes to guest")
		return 0
	}
	results, err := malloc.Call(ctx, uint64(len(data)))
	if err != nil {
		e.logger.Error("failed to call malloc in WASM module", zap.Error(err))
		return 0
	}
	ptr := uint32(results[0])
	if !mod.Memory().Write(ptr, data) {
		e.logger.Error("failed to write to WASM memory")
		return 0
	}
	return (uint64(ptr) << 32) | uint64(len(data))
}

// ReadFromGuest reads a string from guest memory.
func (e *Executor) ReadFromGuest(mod api.Module, ptr, size uint32) ([]byte, bool) {
	return mod.Memory().Read(ptr, size)
}

// UnmarshalJSONFromGuest reads and unmarshals JSON data from guest memory.
func (e *Executor) UnmarshalJSONFromGuest(mod api.Module, ptr, size uint32, v interface{}) error {
	data, ok := mod.Memory().Read(ptr, size)
	if !ok {
		return fmt.Errorf("failed to read from guest memory")
	}
	return json.Unmarshal(data, v)
}
