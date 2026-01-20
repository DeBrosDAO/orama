package serverless

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/serverless/cache"
	"github.com/DeBrosOfficial/network/pkg/serverless/execution"
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

	// Module cache
	moduleCache *cache.ModuleCache

	// Execution components
	executor  *execution.Executor
	lifecycle *execution.ModuleLifecycle

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
		moduleCache:  cache.NewModuleCache(cfg.ModuleCacheSize, logger),
		executor:     execution.NewExecutor(runtime, logger),
		lifecycle:    execution.NewModuleLifecycle(runtime, logger),
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

	invCtx = EnsureInvocationContext(invCtx, fn)
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
	execCtx, cancel := CreateTimeoutContext(ctx, fn, e.config.MaxTimeoutSeconds)
	defer cancel()

	// Get compiled module (from cache or compile)
	module, err := e.getOrCompileModule(execCtx, fn.WASMCID)
	if err != nil {
		e.logInvocation(ctx, fn, invCtx, startTime, 0, InvocationStatusError, err)
		return nil, &ExecutionError{FunctionName: fn.Name, RequestID: invCtx.RequestID, Cause: err}
	}

	// Execute the module with context setters
	var contextSetter, contextClearer func()
	if hf, ok := e.hostServices.(contextAwareHostServices); ok {
		contextSetter = func() { hf.SetInvocationContext(invCtx) }
		contextClearer = func() { hf.ClearContext() }
	}
	output, err := e.executor.ExecuteModule(execCtx, module, fn.Name, input, contextSetter, contextClearer)
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
	if e.moduleCache.Has(wasmCID) {
		return nil
	}

	// Compile the module
	compiled, err := e.lifecycle.CompileModule(ctx, wasmCID, wasmBytes)
	if err != nil {
		return &DeployError{FunctionName: wasmCID, Cause: err}
	}

	// Cache the compiled module
	e.moduleCache.Set(wasmCID, compiled)

	return nil
}

// Invalidate removes a compiled module from the cache.
func (e *Engine) Invalidate(wasmCID string) {
	e.moduleCache.Delete(context.Background(), wasmCID)
}

// Close shuts down the engine and releases resources.
func (e *Engine) Close(ctx context.Context) error {
	// Close all cached modules
	e.moduleCache.Clear(ctx)

	// Close the runtime
	return e.runtime.Close(ctx)
}

// GetCacheStats returns cache statistics.
func (e *Engine) GetCacheStats() (size int, capacity int) {
	return e.moduleCache.GetStats()
}

// -----------------------------------------------------------------------------
// Private methods
// -----------------------------------------------------------------------------

// getOrCompileModule retrieves a compiled module from cache or compiles it.
func (e *Engine) getOrCompileModule(ctx context.Context, wasmCID string) (wazero.CompiledModule, error) {
	return e.moduleCache.GetOrCompute(wasmCID, func() (wazero.CompiledModule, error) {
		// Fetch WASM bytes from registry
		wasmBytes, err := e.registry.GetWASMBytes(ctx, wasmCID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch WASM: %w", err)
		}

		// Compile the module
		compiled, err := e.lifecycle.CompileModule(ctx, wasmCID, wasmBytes)
		if err != nil {
			return nil, ErrCompilationFailed
		}

		return compiled, nil
	})
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

// -----------------------------------------------------------------------------
// Host function implementations (delegate to executor for memory operations)
// -----------------------------------------------------------------------------

func (e *Engine) hGetCallerWallet(ctx context.Context, mod api.Module) uint64 {
	wallet := e.hostServices.GetCallerWallet(ctx)
	return e.executor.WriteToGuest(ctx, mod, []byte(wallet))
}

func (e *Engine) hGetRequestID(ctx context.Context, mod api.Module) uint64 {
	rid := e.hostServices.GetRequestID(ctx)
	return e.executor.WriteToGuest(ctx, mod, []byte(rid))
}

func (e *Engine) hGetEnv(ctx context.Context, mod api.Module, keyPtr, keyLen uint32) uint64 {
	key, ok := e.executor.ReadFromGuest(mod, keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, _ := e.hostServices.GetEnv(ctx, string(key))
	return e.executor.WriteToGuest(ctx, mod, []byte(val))
}

func (e *Engine) hGetSecret(ctx context.Context, mod api.Module, namePtr, nameLen uint32) uint64 {
	name, ok := e.executor.ReadFromGuest(mod, namePtr, nameLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.GetSecret(ctx, string(name))
	if err != nil {
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, []byte(val))
}

func (e *Engine) hDBQuery(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint64 {
	query, ok := e.executor.ReadFromGuest(mod, queryPtr, queryLen)
	if !ok {
		return 0
	}

	var args []interface{}
	if argsLen > 0 {
		if err := e.executor.UnmarshalJSONFromGuest(mod, argsPtr, argsLen, &args); err != nil {
			e.logger.Error("failed to unmarshal db_query arguments", zap.Error(err))
			return 0
		}
	}

	results, err := e.hostServices.DBQuery(ctx, string(query), args)
	if err != nil {
		e.logger.Error("host function db_query failed", zap.Error(err), zap.String("query", string(query)))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, results)
}

func (e *Engine) hDBExecute(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint32 {
	query, ok := e.executor.ReadFromGuest(mod, queryPtr, queryLen)
	if !ok {
		return 0
	}

	var args []interface{}
	if argsLen > 0 {
		if err := e.executor.UnmarshalJSONFromGuest(mod, argsPtr, argsLen, &args); err != nil {
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
	key, ok := e.executor.ReadFromGuest(mod, keyPtr, keyLen)
	if !ok {
		return 0
	}
	val, err := e.hostServices.CacheGet(ctx, string(key))
	if err != nil {
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, val)
}

func (e *Engine) hCacheSet(ctx context.Context, mod api.Module, keyPtr, keyLen, valPtr, valLen uint32, ttl int64) {
	key, ok := e.executor.ReadFromGuest(mod, keyPtr, keyLen)
	if !ok {
		return
	}
	val, ok := e.executor.ReadFromGuest(mod, valPtr, valLen)
	if !ok {
		return
	}
	_ = e.hostServices.CacheSet(ctx, string(key), val, ttl)
}

func (e *Engine) hCacheIncr(ctx context.Context, mod api.Module, keyPtr, keyLen uint32) int64 {
	key, ok := e.executor.ReadFromGuest(mod, keyPtr, keyLen)
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
	key, ok := e.executor.ReadFromGuest(mod, keyPtr, keyLen)
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
	method, ok := e.executor.ReadFromGuest(mod, methodPtr, methodLen)
	if !ok {
		return 0
	}
	u, ok := e.executor.ReadFromGuest(mod, urlPtr, urlLen)
	if !ok {
		return 0
	}

	var headers map[string]string
	if headersLen > 0 {
		if err := e.executor.UnmarshalJSONFromGuest(mod, headersPtr, headersLen, &headers); err != nil {
			e.logger.Error("failed to unmarshal http_fetch headers", zap.Error(err))
			return 0
		}
	}

	body, ok := e.executor.ReadFromGuest(mod, bodyPtr, bodyLen)
	if !ok {
		return 0
	}

	resp, err := e.hostServices.HTTPFetch(ctx, string(method), string(u), headers, body)
	if err != nil {
		e.logger.Error("host function http_fetch failed", zap.Error(err), zap.String("url", string(u)))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, resp)
}

func (e *Engine) hPubSubPublish(ctx context.Context, mod api.Module, topicPtr, topicLen, dataPtr, dataLen uint32) uint32 {
	topic, ok := e.executor.ReadFromGuest(mod, topicPtr, topicLen)
	if !ok {
		return 0
	}

	data, ok := e.executor.ReadFromGuest(mod, dataPtr, dataLen)
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
	msg, ok := e.executor.ReadFromGuest(mod, ptr, size)
	if ok {
		e.hostServices.LogInfo(ctx, string(msg))
	}
}

func (e *Engine) hLogError(ctx context.Context, mod api.Module, ptr, size uint32) {
	msg, ok := e.executor.ReadFromGuest(mod, ptr, size)
	if ok {
		e.hostServices.LogError(ctx, string(msg))
	}
}
