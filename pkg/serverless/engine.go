package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"
)

// contextAwareHostServices is an internal interface for services that need to know about
// the current invocation context.
type contextAwareHostServices interface {
	SetInvocationContext(invCtx *InvocationContext)
	ClearContext()
}

// Ensure Engine implements FunctionExecutor interface.
var _ FunctionExecutor = (*Engine)(nil)

// Engine is the core WASM execution engine using wazero.
// It manages compiled module caching and function execution.
type Engine struct {
	runtime      wazero.Runtime
	config       *Config
	registry     FunctionRegistry
	hostServices HostServices
	logger       *zap.Logger

	// Module cache: wasmCID -> compiled module
	moduleCache   map[string]wazero.CompiledModule
	moduleCacheMu sync.RWMutex

	// Invocation logger for metrics/debugging
	invocationLogger InvocationLogger

	// Rate limiter
	rateLimiter RateLimiter
}

// InvocationLogger logs function invocations (optional).
type InvocationLogger interface {
	Log(ctx context.Context, inv *InvocationRecord) error
}

// InvocationRecord represents a logged invocation.
type InvocationRecord struct {
	ID           string           `json:"id"`
	FunctionID   string           `json:"function_id"`
	RequestID    string           `json:"request_id"`
	TriggerType  TriggerType      `json:"trigger_type"`
	CallerWallet string           `json:"caller_wallet,omitempty"`
	InputSize    int              `json:"input_size"`
	OutputSize   int              `json:"output_size"`
	StartedAt    time.Time        `json:"started_at"`
	CompletedAt  time.Time        `json:"completed_at"`
	DurationMS   int64            `json:"duration_ms"`
	Status       InvocationStatus `json:"status"`
	ErrorMessage string           `json:"error_message,omitempty"`
	MemoryUsedMB float64          `json:"memory_used_mb"`
	Logs         []LogEntry       `json:"logs,omitempty"`
}

// RateLimiter checks if a request should be rate limited.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// EngineOption configures the Engine.
type EngineOption func(*Engine)

// WithInvocationLogger sets the invocation logger.
func WithInvocationLogger(logger InvocationLogger) EngineOption {
	return func(e *Engine) {
		e.invocationLogger = logger
	}
}

// WithRateLimiter sets the rate limiter.
func WithRateLimiter(limiter RateLimiter) EngineOption {
	return func(e *Engine) {
		e.rateLimiter = limiter
	}
}

// NewEngine creates a new WASM execution engine.
func NewEngine(cfg *Config, registry FunctionRegistry, hostServices HostServices, logger *zap.Logger, opts ...EngineOption) (*Engine, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	cfg.ApplyDefaults()

	// Create wazero runtime with compilation cache
	runtimeConfig := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true)

	runtime := wazero.NewRuntimeWithConfig(context.Background(), runtimeConfig)

	// Instantiate WASI - required for WASM modules compiled with TinyGo targeting WASI
	wasi_snapshot_preview1.MustInstantiate(context.Background(), runtime)

	engine := &Engine{
		runtime:      runtime,
		config:       cfg,
		registry:     registry,
		hostServices: hostServices,
		logger:       logger,
		moduleCache:  make(map[string]wazero.CompiledModule),
	}

	// Apply options
	for _, opt := range opts {
		opt(engine)
	}

	// Register host functions
	if err := engine.registerHostModule(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to register host module: %w", err)
	}

	return engine, nil
}

// Execute runs a function with the given input and returns the output.
func (e *Engine) Execute(ctx context.Context, fn *Function, input []byte, invCtx *InvocationContext) ([]byte, error) {
	if fn == nil {
		return nil, &ValidationError{Field: "function", Message: "cannot be nil"}
	}
	if invCtx == nil {
		invCtx = &InvocationContext{
			RequestID:    uuid.New().String(),
			FunctionID:   fn.ID,
			FunctionName: fn.Name,
			Namespace:    fn.Namespace,
			TriggerType:  TriggerTypeHTTP,
		}
	}

	startTime := time.Now()

	// Check rate limit
	if e.rateLimiter != nil {
		allowed, err := e.rateLimiter.Allow(ctx, "global")
		if err != nil {
			e.logger.Warn("Rate limiter error", zap.Error(err))
		} else if !allowed {
			return nil, ErrRateLimited
		}
	}

	// Create timeout context
	timeout := time.Duration(fn.TimeoutSeconds) * time.Second
	if timeout > time.Duration(e.config.MaxTimeoutSeconds)*time.Second {
		timeout = time.Duration(e.config.MaxTimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Get compiled module (from cache or compile)
	module, err := e.getOrCompileModule(execCtx, fn.WASMCID)
	if err != nil {
		e.logInvocation(ctx, fn, invCtx, startTime, 0, InvocationStatusError, err)
		return nil, &ExecutionError{FunctionName: fn.Name, RequestID: invCtx.RequestID, Cause: err}
	}

	// Execute the module
	output, err := e.executeModule(execCtx, module, fn, input, invCtx)
	if err != nil {
		status := InvocationStatusError
		if execCtx.Err() == context.DeadlineExceeded {
			status = InvocationStatusTimeout
			err = ErrTimeout
		}
		e.logInvocation(ctx, fn, invCtx, startTime, len(output), status, err)
		return nil, &ExecutionError{FunctionName: fn.Name, RequestID: invCtx.RequestID, Cause: err}
	}

	e.logInvocation(ctx, fn, invCtx, startTime, len(output), InvocationStatusSuccess, nil)
	return output, nil
}

// Precompile compiles a WASM module and caches it for faster execution.
func (e *Engine) Precompile(ctx context.Context, wasmCID string, wasmBytes []byte) error {
	if wasmCID == "" {
		return &ValidationError{Field: "wasmCID", Message: "cannot be empty"}
	}
	if len(wasmBytes) == 0 {
		return &ValidationError{Field: "wasmBytes", Message: "cannot be empty"}
	}

	// Check if already cached
	e.moduleCacheMu.RLock()
	_, exists := e.moduleCache[wasmCID]
	e.moduleCacheMu.RUnlock()
	if exists {
		return nil
	}

	// Compile the module
	compiled, err := e.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return &DeployError{FunctionName: wasmCID, Cause: fmt.Errorf("failed to compile WASM: %w", err)}
	}

	// Cache the compiled module
	e.moduleCacheMu.Lock()
	defer e.moduleCacheMu.Unlock()

	// Evict oldest if cache is full
	if len(e.moduleCache) >= e.config.ModuleCacheSize {
		e.evictOldestModule()
	}

	e.moduleCache[wasmCID] = compiled

	e.logger.Debug("Module precompiled and cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(e.moduleCache)),
	)

	return nil
}

// Invalidate removes a compiled module from the cache.
func (e *Engine) Invalidate(wasmCID string) {
	e.moduleCacheMu.Lock()
	defer e.moduleCacheMu.Unlock()

	if module, exists := e.moduleCache[wasmCID]; exists {
		_ = module.Close(context.Background())
		delete(e.moduleCache, wasmCID)
		e.logger.Debug("Module invalidated from cache", zap.String("wasm_cid", wasmCID))
	}
}

// Close shuts down the engine and releases resources.
func (e *Engine) Close(ctx context.Context) error {
	e.moduleCacheMu.Lock()
	defer e.moduleCacheMu.Unlock()

	// Close all cached modules
	for cid, module := range e.moduleCache {
		if err := module.Close(ctx); err != nil {
			e.logger.Warn("Failed to close cached module", zap.String("cid", cid), zap.Error(err))
		}
	}
	e.moduleCache = make(map[string]wazero.CompiledModule)

	// Close the runtime
	return e.runtime.Close(ctx)
}

// GetCacheStats returns cache statistics.
func (e *Engine) GetCacheStats() (size int, capacity int) {
	e.moduleCacheMu.RLock()
	defer e.moduleCacheMu.RUnlock()
	return len(e.moduleCache), e.config.ModuleCacheSize
}

// -----------------------------------------------------------------------------
// Private methods
// -----------------------------------------------------------------------------

// getOrCompileModule retrieves a compiled module from cache or compiles it.
func (e *Engine) getOrCompileModule(ctx context.Context, wasmCID string) (wazero.CompiledModule, error) {
	// Check cache first
	e.moduleCacheMu.RLock()
	if module, exists := e.moduleCache[wasmCID]; exists {
		e.moduleCacheMu.RUnlock()
		return module, nil
	}
	e.moduleCacheMu.RUnlock()

	// Fetch WASM bytes from registry
	wasmBytes, err := e.registry.GetWASMBytes(ctx, wasmCID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch WASM: %w", err)
	}

	// Compile the module
	compiled, err := e.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, ErrCompilationFailed
	}

	// Cache the compiled module
	e.moduleCacheMu.Lock()
	defer e.moduleCacheMu.Unlock()

	// Double-check (another goroutine might have added it)
	if existingModule, exists := e.moduleCache[wasmCID]; exists {
		_ = compiled.Close(ctx) // Discard our compilation
		return existingModule, nil
	}

	// Evict if cache is full
	if len(e.moduleCache) >= e.config.ModuleCacheSize {
		e.evictOldestModule()
	}

	e.moduleCache[wasmCID] = compiled

	e.logger.Debug("Module compiled and cached",
		zap.String("wasm_cid", wasmCID),
		zap.Int("cache_size", len(e.moduleCache)),
	)

	return compiled, nil
}

// executeModule instantiates and runs a WASM module.
func (e *Engine) executeModule(ctx context.Context, compiled wazero.CompiledModule, fn *Function, input []byte, invCtx *InvocationContext) ([]byte, error) {
	// Set invocation context for host functions if the service supports it
	if hf, ok := e.hostServices.(contextAwareHostServices); ok {
		hf.SetInvocationContext(invCtx)
		defer hf.ClearContext()
	}

	// Create buffers for stdin/stdout (WASI uses these for I/O)
	stdin := bytes.NewReader(input)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// Create module configuration with WASI stdio
	moduleConfig := wazero.NewModuleConfig().
		WithName(fn.Name).
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithArgs(fn.Name) // argv[0] is the program name

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

// callHandleFunction calls the main 'handle' export in the WASM module.
func (e *Engine) callHandleFunction(ctx context.Context, instance api.Module, input []byte, invCtx *InvocationContext) ([]byte, error) {
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

// evictOldestModule removes the oldest module from cache.
// Must be called with moduleCacheMu held.
func (e *Engine) evictOldestModule() {
	// Simple LRU: just remove the first one we find
	// In production, you'd want proper LRU tracking
	for cid, module := range e.moduleCache {
		_ = module.Close(context.Background())
		delete(e.moduleCache, cid)
		e.logger.Debug("Evicted module from cache", zap.String("wasm_cid", cid))
		break
	}
}

// logInvocation logs an invocation record.
func (e *Engine) logInvocation(ctx context.Context, fn *Function, invCtx *InvocationContext, startTime time.Time, outputSize int, status InvocationStatus, err error) {
	if e.invocationLogger == nil || !e.config.LogInvocations {
		return
	}

	completedAt := time.Now()
	record := &InvocationRecord{
		ID:           uuid.New().String(),
		FunctionID:   fn.ID,
		RequestID:    invCtx.RequestID,
		TriggerType:  invCtx.TriggerType,
		CallerWallet: invCtx.CallerWallet,
		OutputSize:   outputSize,
		StartedAt:    startTime,
		CompletedAt:  completedAt,
		DurationMS:   completedAt.Sub(startTime).Milliseconds(),
		Status:       status,
	}

	if err != nil {
		record.ErrorMessage = err.Error()
	}

	// Collect logs from host services if supported
	if hf, ok := e.hostServices.(interface{ GetLogs() []LogEntry }); ok {
		record.Logs = hf.GetLogs()
	}

	if logErr := e.invocationLogger.Log(ctx, record); logErr != nil {
		e.logger.Warn("Failed to log invocation", zap.Error(logErr))
	}
}

// registerHostModule registers the Orama host functions with the wazero runtime.
func (e *Engine) registerHostModule(ctx context.Context) error {
	// Register under both "env" and "host" to support different import styles
	// The user requested "env" in instructions but "host" in expected result.
	for _, moduleName := range []string{"env", "host"} {
		_, err := e.runtime.NewHostModuleBuilder(moduleName).
			NewFunctionBuilder().WithFunc(e.hGetCallerWallet).Export("get_caller_wallet").
			NewFunctionBuilder().WithFunc(e.hGetRequestID).Export("get_request_id").
			NewFunctionBuilder().WithFunc(e.hGetEnv).Export("get_env").
			NewFunctionBuilder().WithFunc(e.hGetSecret).Export("get_secret").
			NewFunctionBuilder().WithFunc(e.hDBQuery).Export("db_query").
			NewFunctionBuilder().WithFunc(e.hDBExecute).Export("db_execute").
			NewFunctionBuilder().WithFunc(e.hCacheGet).Export("cache_get").
			NewFunctionBuilder().WithFunc(e.hCacheSet).Export("cache_set").
			NewFunctionBuilder().WithFunc(e.hCacheIncr).Export("cache_incr").
			NewFunctionBuilder().WithFunc(e.hCacheIncrBy).Export("cache_incr_by").
			NewFunctionBuilder().WithFunc(e.hHTTPFetch).Export("http_fetch").
			NewFunctionBuilder().WithFunc(e.hPubSubPublish).Export("pubsub_publish").
			NewFunctionBuilder().WithFunc(e.hLogInfo).Export("log_info").
			NewFunctionBuilder().WithFunc(e.hLogError).Export("log_error").
			Instantiate(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) hGetCallerWallet(ctx context.Context, mod api.Module) uint64 {
	wallet := e.hostServices.GetCallerWallet(ctx)
	return e.writeToGuest(ctx, mod, []byte(wallet))
}

func (e *Engine) hGetRequestID(ctx context.Context, mod api.Module) uint64 {
	rid := e.hostServices.GetRequestID(ctx)
	return e.writeToGuest(ctx, mod, []byte(rid))
}

func (e *Engine) hGetEnv(ctx context.Context, mod api.Module, keyPtr, keyLen uint32) uint64 {
	key, ok := mod.Memory().Read(keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, _ := e.hostServices.GetEnv(ctx, string(key))
	return e.writeToGuest(ctx, mod, []byte(val))
}

func (e *Engine) hGetSecret(ctx context.Context, mod api.Module, namePtr, nameLen uint32) uint64 {
	name, ok := mod.Memory().Read(namePtr, nameLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.GetSecret(ctx, string(name))
	if err != nil {
		return 0
	}
	return e.writeToGuest(ctx, mod, []byte(val))
}

func (e *Engine) hDBQuery(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint64 {
	query, ok := mod.Memory().Read(queryPtr, queryLen)
	if !ok {
		return 0
	}

	var args []interface{}
	if argsLen > 0 {
		argsData, ok := mod.Memory().Read(argsPtr, argsLen)
		if !ok {
			return 0
		}
		if err := json.Unmarshal(argsData, &args); err != nil {
			e.logger.Error("failed to unmarshal db_query arguments", zap.Error(err))
			return 0
		}
	}

	results, err := e.hostServices.DBQuery(ctx, string(query), args)
	if err != nil {
		e.logger.Error("host function db_query failed", zap.Error(err), zap.String("query", string(query)))
		return 0
	}
	return e.writeToGuest(ctx, mod, results)
}

func (e *Engine) hDBExecute(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint32 {
	query, ok := mod.Memory().Read(queryPtr, queryLen)
	if !ok {
		return 0
	}

	var args []interface{}
	if argsLen > 0 {
		argsData, ok := mod.Memory().Read(argsPtr, argsLen)
		if !ok {
			return 0
		}
		if err := json.Unmarshal(argsData, &args); err != nil {
			e.logger.Error("failed to unmarshal db_execute arguments", zap.Error(err))
			return 0
		}
	}

	affected, err := e.hostServices.DBExecute(ctx, string(query), args)
	if err != nil {
		e.logger.Error("host function db_execute failed", zap.Error(err), zap.String("query", string(query)))
		return 0
	}
	return uint32(affected)
}

func (e *Engine) hCacheGet(ctx context.Context, mod api.Module, keyPtr, keyLen uint32) uint64 {
	key, ok := mod.Memory().Read(keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.CacheGet(ctx, string(key))
	if err != nil {
		return 0
	}
	return e.writeToGuest(ctx, mod, val)
}

func (e *Engine) hCacheSet(ctx context.Context, mod api.Module, keyPtr, keyLen, valPtr, valLen uint32, ttl int64) {
	key, ok := mod.Memory().Read(keyPtr, keyLen)
	if !ok {
		return
	}
	val, ok := mod.Memory().Read(valPtr, valLen)
	if !ok {
		return
	}
	_ = e.hostServices.CacheSet(ctx, string(key), val, ttl)
}

func (e *Engine) hCacheIncr(ctx context.Context, mod api.Module, keyPtr, keyLen uint32) int64 {
	key, ok := mod.Memory().Read(keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.CacheIncr(ctx, string(key))
	if err != nil {
		e.logger.Error("host function cache_incr failed", zap.Error(err), zap.String("key", string(key)))
		return 0
	}
	return val
}

func (e *Engine) hCacheIncrBy(ctx context.Context, mod api.Module, keyPtr, keyLen uint32, delta int64) int64 {
	key, ok := mod.Memory().Read(keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.CacheIncrBy(ctx, string(key), delta)
	if err != nil {
		e.logger.Error("host function cache_incr_by failed", zap.Error(err), zap.String("key", string(key)), zap.Int64("delta", delta))
		return 0
	}
	return val
}

func (e *Engine) hHTTPFetch(ctx context.Context, mod api.Module, methodPtr, methodLen, urlPtr, urlLen, headersPtr, headersLen, bodyPtr, bodyLen uint32) uint64 {
	method, ok := mod.Memory().Read(methodPtr, methodLen)
	if !ok {
		return 0
	}
	u, ok := mod.Memory().Read(urlPtr, urlLen)
	if !ok {
		return 0
	}
	var headers map[string]string
	if headersLen > 0 {
		headersData, ok := mod.Memory().Read(headersPtr, headersLen)
		if !ok {
			return 0
		}
		if err := json.Unmarshal(headersData, &headers); err != nil {
			e.logger.Error("failed to unmarshal http_fetch headers", zap.Error(err))
			return 0
		}
	}
	body, ok := mod.Memory().Read(bodyPtr, bodyLen)
	if !ok {
		return 0
	}

	resp, err := e.hostServices.HTTPFetch(ctx, string(method), string(u), headers, body)
	if err != nil {
		e.logger.Error("host function http_fetch failed", zap.Error(err), zap.String("url", string(u)))
		return 0
	}
	return e.writeToGuest(ctx, mod, resp)
}

func (e *Engine) hPubSubPublish(ctx context.Context, mod api.Module, topicPtr, topicLen, dataPtr, dataLen uint32) uint32 {
	topic, ok := mod.Memory().Read(topicPtr, topicLen)
	if !ok {
		return 0
	}

	data, ok := mod.Memory().Read(dataPtr, dataLen)
	if !ok {
		return 0
	}

	err := e.hostServices.PubSubPublish(ctx, string(topic), data)
	if err != nil {
		e.logger.Error("host function pubsub_publish failed", zap.Error(err), zap.String("topic", string(topic)))
		return 0
	}
	return 1 // Success
}

func (e *Engine) hLogInfo(ctx context.Context, mod api.Module, ptr, size uint32) {
	msg, ok := mod.Memory().Read(ptr, size)
	if ok {
		e.hostServices.LogInfo(ctx, string(msg))
	}
}

func (e *Engine) hLogError(ctx context.Context, mod api.Module, ptr, size uint32) {
	msg, ok := mod.Memory().Read(ptr, size)
	if ok {
		e.hostServices.LogError(ctx, string(msg))
	}
}

func (e *Engine) writeToGuest(ctx context.Context, mod api.Module, data []byte) uint64 {
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
