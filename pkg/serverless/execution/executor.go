package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.uber.org/zap"
)

// Executor handles WASM module execution.
type Executor struct {
	runtime wazero.Runtime
	logger  *zap.Logger
}

// NewExecutor creates a new Executor.
func NewExecutor(runtime wazero.Runtime, logger *zap.Logger) *Executor {
	return &Executor{
		runtime: runtime,
		logger:  logger,
	}
}

// ExecuteModule instantiates and runs a WASM module with the given input.
// The contextSetter callback is used to set invocation context on host services.
func (e *Executor) ExecuteModule(ctx context.Context, compiled wazero.CompiledModule, moduleName string, input []byte, contextSetter func(), contextClearer func()) ([]byte, error) {
	// Set invocation context for host functions
	if contextSetter != nil {
		contextSetter()
		if contextClearer != nil {
			defer contextClearer()
		}
	}

	// Create buffers for stdin/stdout (WASI uses these for I/O)
	stdin := bytes.NewReader(input)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// Create module configuration with WASI stdio
	moduleConfig := wazero.NewModuleConfig().
		WithName(moduleName).
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithArgs(moduleName) // argv[0] is the program name

	// Instantiate and run the module (WASI _start will be called automatically)
	instance, err := e.runtime.InstantiateModule(ctx, compiled, moduleConfig)
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
