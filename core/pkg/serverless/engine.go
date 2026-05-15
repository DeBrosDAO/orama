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

// RateLimiter is the legacy single-bucket rate-limit interface, kept for
// backward compatibility with TokenBucketLimiter. New limiters should
// implement TieredRateLimiter as well — the engine prefers the richer path
// when available via type assertion.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

// TieredRateLimiter is the rich interface that lets the engine pass
// per-(namespace, function, wallet, ip) context for layered enforcement.
// MultiTierLimiter implements both this and the legacy RateLimiter.
type TieredRateLimiter interface {
	AllowRequest(ctx context.Context, req RateLimitRequest) (Decision, error)
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
		executor:     execution.NewExecutor(runtime, logger, cfg.MaxConcurrentExecutions),
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

	// Check rate limit. Prefer the tiered path when the limiter supports it
	// — that gives per-(ns, fn, wallet, ip) enforcement with retry-after.
	// Fall back to the legacy single-bucket interface otherwise.
	if e.rateLimiter != nil {
		if tl, ok := e.rateLimiter.(TieredRateLimiter); ok {
			req := RateLimitRequest{
				Namespace: invCtx.Namespace,
				Function:  invCtx.FunctionName,
				Wallet:    invCtx.CallerWallet,
				IP:        invCtx.CallerIP,
			}
			d, err := tl.AllowRequest(ctx, req)
			if err != nil {
				e.logger.Warn("Rate limiter error", zap.Error(err))
			} else if !d.Allowed {
				return nil, &RateLimitedError{Scope: d.Scope, RetryAfter: d.RetryAfter}
			}
		} else {
			allowed, err := e.rateLimiter.Allow(ctx, "global")
			if err != nil {
				e.logger.Warn("Rate limiter error", zap.Error(err))
			} else if !allowed {
				return nil, ErrRateLimited
			}
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

	// Enforce memory limits
	if err := e.checkMemoryLimits(compiled); err != nil {
		compiled.Close(ctx)
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

// checkMemoryLimits validates that a compiled module's memory declarations
// don't exceed the configured maximum. Each WASM memory page is 64KB.
func (e *Engine) checkMemoryLimits(compiled wazero.CompiledModule) error {
	maxPages := uint32(e.config.MaxMemoryLimitMB * 16) // 1 MB = 16 pages (64KB each)
	for _, mem := range compiled.ExportedMemories() {
		if max, hasMax := mem.Max(); hasMax && max > maxPages {
			return fmt.Errorf("module declares %d MB max memory, exceeds limit of %d MB",
				max/16, e.config.MaxMemoryLimitMB)
		}
	}
	return nil
}

// getOrCompileModule retrieves a compiled module from cache or compiles it.
// InstantiatePersistent creates a long-lived module instance for a
// persistent WebSocket function. Unlike the per-frame stateless model,
// this instance:
//
//   - is NOT closed after a single call
//   - has its WASI _start hook DISABLED (the function's main() must be
//     empty; the lifecycle exports ws_open/ws_frame/ws_close are called
//     explicitly by the caller)
//   - retains memory across frames
//
// Caller is responsible for calling Close() on the returned api.Module
// (typically wrapped in persistent.Instance which handles this).
func (e *Engine) InstantiatePersistent(ctx context.Context, fn *Function, invCtx *InvocationContext) (api.Module, error) {
	compiled, err := e.getOrCompileModule(ctx, fn.WASMCID)
	if err != nil {
		return nil, fmt.Errorf("InstantiatePersistent: compile: %w", err)
	}

	// Bind invocation context once at instantiation. Subsequent ws_open /
	// ws_frame calls will see this same context (host services read from
	// the bound invCtx). For multi-call lifecycles this is a sticky
	// per-instance context, NOT a per-call context.
	if hf, ok := e.hostServices.(contextAwareHostServices); ok {
		hf.SetInvocationContext(invCtx)
	}

	// Persistent-instance runtime-init policy. TinyGo emits one of two
	// start hooks depending on the build target:
	//
	//   - wasi-reactor target → exports `_initialize` only
	//   - wasi (command) target → exports `_start` only
	//
	// Both hooks run the runtime's initAll (heap, GC, package init).
	// `_start` additionally calls `main()` — fine when main is an
	// empty stub (which is the convention for persistent WS functions
	// since the gateway drives lifecycle via ws_open / ws_frame /
	// ws_close, NOT main()).
	//
	// Without one of them being called, TinyGo's runtime stays in an
	// uninitialized state and the very first export call traps via
	// `wasmExportCheckRun` — managed-memory operations (allocs,
	// hashmap ops) panic immediately.
	//
	// History of this code path (bugs #240/#249 follow-ups):
	//   - Original code: `WithStartFunctions()` with NO args
	//     (explicitly disable both). Intent was to skip main(); side
	//     effect was breaking TinyGo init. Persistent WS dead since
	//     plan #06 landed.
	//   - First fix: call `_initialize` manually. Worked for
	//     wasi-reactor builds. Still broken for wasi (command) builds
	//     like AnChat's rpc-router which only exports `_start`.
	//   - This fix: try `_initialize` first; fall back to `_start`
	//     if reactor hook isn't exported. Bounded by a 5s timeout so
	//     a runaway main() can't hang instantiation forever.
	//
	// AnChat's wasm-objdump output that pinned this:
	//   Export[15]:
	//    - func[127] <_start>                          → "_start"
	//    - func[414] <main.oramaAlloc#wasmexport>      → "orama_alloc"
	//    - func[416] <main.wsOpen#wasmexport>          → "ws_open"
	//    ...
	//   (no `_initialize`)
	//
	// We still pass `WithStartFunctions()` (no args) so wazero doesn't
	// auto-call `_start` during InstantiateModule — we want full
	// control over which hook runs and to bound it with our own
	// timeout.
	moduleConfig := wazero.NewModuleConfig().
		WithName(fn.Name + "-" + invCtx.WSClientID).
		WithStartFunctions().
		WithStdin(emptyReader{}).
		WithStdout(discardWriter{}).
		WithStderr(discardWriter{}).
		WithArgs(fn.Name)

	instance, err := e.runtime.InstantiateModule(ctx, compiled, moduleConfig)
	if err != nil {
		if hf, ok := e.hostServices.(contextAwareHostServices); ok {
			hf.ClearContext()
		}
		return nil, fmt.Errorf("InstantiatePersistent: instantiate: %w", err)
	}

	// Bootstrap the wasm runtime. Try reactor hook first (no main()),
	// then command hook (assumes main() is an empty stub per
	// persistent-function convention). Bounded by a short timeout so
	// a buggy main() can't hang every connection.
	const initTimeout = 5 * time.Second
	initCtx, initCancel := context.WithTimeout(ctx, initTimeout)
	defer initCancel()

	var initName string
	var initFn api.Function
	if hook := instance.ExportedFunction("_initialize"); hook != nil {
		initName, initFn = "_initialize", hook
	} else if hook := instance.ExportedFunction("_start"); hook != nil {
		initName, initFn = "_start", hook
	}
	if initFn != nil {
		if _, callErr := initFn.Call(initCtx); callErr != nil {
			_ = instance.Close(ctx)
			if hf, ok := e.hostServices.(contextAwareHostServices); ok {
				hf.ClearContext()
			}
			return nil, fmt.Errorf("InstantiatePersistent: %s: %w", initName, callErr)
		}
		e.logger.Debug("persistent instance bootstrapped",
			zap.String("function", fn.Name),
			zap.String("client_id", invCtx.WSClientID),
			zap.String("init_hook", initName))
	} else {
		// Neither hook exported. The module may still work if it has
		// no managed-memory operations — but that's rare in TinyGo.
		// Log a warning so a function author who hits this can
		// diagnose without filing a ticket.
		e.logger.Warn("persistent module exports no _initialize or _start; runtime may be uninitialized",
			zap.String("function", fn.Name),
			zap.String("client_id", invCtx.WSClientID))
	}

	return instance, nil
}

// emptyReader satisfies io.Reader for persistent WASM stdin.
type emptyReader struct{}

func (emptyReader) Read(p []byte) (int, error) { return 0, nil }

// discardWriter satisfies io.Writer for persistent WASM stdout/stderr.
// Unlike io.Discard which has special handling, this is a typed value
// suitable for the wazero ModuleConfig API.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

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

		// Enforce memory limits
		if err := e.checkMemoryLimits(compiled); err != nil {
			compiled.Close(ctx)
			return nil, err
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
//
// We expose the SAME export set under three module names:
//
//   - "env"    — canonical. Matches the WASI / TinyGo convention. The
//                official SDK examples and docs use this name.
//   - "host"   — long-standing alias kept for backward compatibility.
//   - "orama"  — alias added 2026-05-06 after multiple apps intuited the
//                brand name as the import target and hit cryptic
//                "module[orama] not instantiated" errors. Cheap insurance:
//                a few KB of runtime metadata per alias, zero behavioral
//                cost. Apps SHOULD prefer `env` going forward; `orama` is
//                supported indefinitely to avoid breaking deployed code.
//
// All three names resolve to identical function tables — a WASM module
// can mix imports across the three with no consequence.
func (e *Engine) registerHostModule(ctx context.Context) error {
	for _, moduleName := range []string{"env", "host", "orama"} {
		_, err := e.runtime.NewHostModuleBuilder(moduleName).
			NewFunctionBuilder().WithFunc(e.hGetCallerWallet).Export("get_caller_wallet").
			NewFunctionBuilder().WithFunc(e.hGetCallerJWTSubject).Export("get_caller_jwt_subject").
			NewFunctionBuilder().WithFunc(e.hGetWSClientID).Export("get_ws_client_id").
			NewFunctionBuilder().WithFunc(e.hGetCallerClaim).Export("get_caller_claim").
			NewFunctionBuilder().WithFunc(e.hGetRequestID).Export("get_request_id").
			NewFunctionBuilder().WithFunc(e.hGetEnv).Export("get_env").
			NewFunctionBuilder().WithFunc(e.hGetSecret).Export("get_secret").
			NewFunctionBuilder().WithFunc(e.hDBQuery).Export("db_query").
			NewFunctionBuilder().WithFunc(e.hDBQueryV2).Export("db_query_v2").
			NewFunctionBuilder().WithFunc(e.hDBExecute).Export("db_execute").
			NewFunctionBuilder().WithFunc(e.hDBExecuteV2).Export("db_execute_v2").
			NewFunctionBuilder().WithFunc(e.hDBTransaction).Export("db_transaction").
			NewFunctionBuilder().WithFunc(e.hExecAndPublish).Export("exec_and_publish").
			NewFunctionBuilder().WithFunc(e.hCacheGet).Export("cache_get").
			NewFunctionBuilder().WithFunc(e.hCacheSet).Export("cache_set").
			NewFunctionBuilder().WithFunc(e.hCacheIncr).Export("cache_incr").
			NewFunctionBuilder().WithFunc(e.hCacheIncrBy).Export("cache_incr_by").
			NewFunctionBuilder().WithFunc(e.hHTTPFetch).Export("http_fetch").
			NewFunctionBuilder().WithFunc(e.hPubSubPublish).Export("pubsub_publish").
			NewFunctionBuilder().WithFunc(e.hPubSubPublishBatch).Export("pubsub_publish_batch").
			NewFunctionBuilder().WithFunc(e.hPushSend).Export("push_send").
			NewFunctionBuilder().WithFunc(e.hWSPubSubBridge).Export("ws_pubsub_bridge").
			NewFunctionBuilder().WithFunc(e.hWSPubSubUnbridge).Export("ws_pubsub_unbridge").
			NewFunctionBuilder().WithFunc(e.hWSSend).Export("ws_send").
			NewFunctionBuilder().WithFunc(e.hWSBroadcast).Export("ws_broadcast").
			NewFunctionBuilder().WithFunc(e.hFunctionInvoke).Export("function_invoke").
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

// hGetWSClientID returns the current invocation's WebSocket client ID, or
// empty string if the function wasn't invoked via WS.
func (e *Engine) hGetWSClientID(ctx context.Context, mod api.Module) uint64 {
	cid := e.hostServices.GetWSClientID(ctx)
	return e.executor.WriteToGuest(ctx, mod, []byte(cid))
}

// hGetCallerJWTSubject returns the JWT `sub` claim explicitly. Empty
// string if the request was not JWT-authenticated. See bug #215.
func (e *Engine) hGetCallerJWTSubject(ctx context.Context, mod api.Module) uint64 {
	sub := e.hostServices.GetCallerJWTSubject(ctx)
	return e.executor.WriteToGuest(ctx, mod, []byte(sub))
}

// hGetCallerClaim reads a claim name from guest memory, looks it up on the
// caller's JWT custom claims, and writes the value (or empty string) back.
func (e *Engine) hGetCallerClaim(ctx context.Context, mod api.Module, namePtr, nameLen uint32) uint64 {
	name, ok := e.executor.ReadFromGuest(mod, namePtr, nameLen)
	if !ok {
		return 0
	}
	val := e.hostServices.GetCallerClaim(ctx, string(name))
	return e.executor.WriteToGuest(ctx, mod, []byte(val))
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

// hPubSubPublishBatch is the WASM-callable wrapper for PubSubPublishBatch.
// Input: pointer/length of a JSON array of {topic, data_base64}.
// Returns 1 on success, 0 on error.
func (e *Engine) hPubSubPublishBatch(ctx context.Context, mod api.Module, msgsPtr, msgsLen uint32) uint32 {
	msgsJSON, ok := e.executor.ReadFromGuest(mod, msgsPtr, msgsLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.PubSubPublishBatch(ctx, msgsJSON); err != nil {
		e.logger.Error("host function pubsub_publish_batch failed", zap.Error(err))
		return 0
	}
	return 1
}

// hDBExecuteV2 is the WASM-callable wrapper for DBExecuteV2 (bug #218).
// Inputs: pointer/length of SQL + JSON args. Returns a packed uint64
// (ptr<<32 | len) pointing to the JSON envelope in guest memory, or 0 on
// host-side failure. The JSON envelope's "error" field carries SQL errors.
func (e *Engine) hDBExecuteV2(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint64 {
	query, ok := e.executor.ReadFromGuest(mod, queryPtr, queryLen)
	if !ok {
		return 0
	}
	var args []interface{}
	if argsLen > 0 {
		if err := e.executor.UnmarshalJSONFromGuest(mod, argsPtr, argsLen, &args); err != nil {
			e.logger.Warn("db_execute_v2: failed to unmarshal args", zap.Error(err))
			return 0
		}
	}
	out, err := e.hostServices.DBExecuteV2(ctx, string(query), args)
	if err != nil {
		e.logger.Warn("host function db_execute_v2 failed", zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, out)
}

// hDBQueryV2 is the WASM-callable wrapper for DBQueryV2 (bug #218).
func (e *Engine) hDBQueryV2(ctx context.Context, mod api.Module, queryPtr, queryLen, argsPtr, argsLen uint32) uint64 {
	query, ok := e.executor.ReadFromGuest(mod, queryPtr, queryLen)
	if !ok {
		return 0
	}
	var args []interface{}
	if argsLen > 0 {
		if err := e.executor.UnmarshalJSONFromGuest(mod, argsPtr, argsLen, &args); err != nil {
			e.logger.Warn("db_query_v2: failed to unmarshal args", zap.Error(err))
			return 0
		}
	}
	out, err := e.hostServices.DBQueryV2(ctx, string(query), args)
	if err != nil {
		e.logger.Warn("host function db_query_v2 failed", zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, out)
}

// hDBTransaction is the WASM-callable wrapper for DBTransaction.
// Input: pointer/length of opsJSON ({"ops":[{kind,sql,args},...]}).
// Returns a packed uint64 (ptr<<32 | len) pointing to JSON BatchResult in
// guest memory, or 0 on setup error.
//
// Note the result JSON's `committed` field tells the caller whether the
// writes landed — a return of non-zero does NOT imply commit.
func (e *Engine) hDBTransaction(ctx context.Context, mod api.Module, opsPtr, opsLen uint32) uint64 {
	opsJSON, ok := e.executor.ReadFromGuest(mod, opsPtr, opsLen)
	if !ok {
		return 0
	}
	out, err := e.hostServices.DBTransaction(ctx, opsJSON)
	if err != nil {
		e.logger.Warn("host function db_transaction failed", zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, out)
}

// hExecAndPublish is the WASM-callable wrapper for ExecAndPublish.
// Inputs:
//
//	opsPtr/opsLen     — JSON {"ops":[{kind,sql,args},...]}
//	topicPtr/topicLen — UTF-8 PubSub topic for the wake-up
//	dataPtr/dataLen   — wake-up payload bytes; "{{seq}}" will be substituted
//
// Returns a packed uint64 (ptr<<32 | len) pointing to the JSON result in
// guest memory, or 0 on setup error. The result JSON has fields
// committed/seq/published/publish_error that the caller inspects.
func (e *Engine) hExecAndPublish(ctx context.Context, mod api.Module,
	opsPtr, opsLen, topicPtr, topicLen, dataPtr, dataLen uint32) uint64 {

	opsJSON, ok := e.executor.ReadFromGuest(mod, opsPtr, opsLen)
	if !ok {
		return 0
	}
	topic, ok := e.executor.ReadFromGuest(mod, topicPtr, topicLen)
	if !ok {
		return 0
	}
	data, ok := e.executor.ReadFromGuest(mod, dataPtr, dataLen)
	if !ok {
		return 0
	}
	out, err := e.hostServices.ExecAndPublish(ctx, opsJSON, string(topic), data)
	if err != nil {
		e.logger.Warn("host function exec_and_publish failed",
			zap.String("topic", string(topic)),
			zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, out)
}

// hWSPubSubBridge is the WASM-callable wrapper for WSPubSubBridge.
// Inputs: clientID + topic strings. Returns 1 on success, 0 on error.
func (e *Engine) hWSPubSubBridge(ctx context.Context, mod api.Module,
	cidPtr, cidLen, topicPtr, topicLen uint32) uint32 {
	cid, ok := e.executor.ReadFromGuest(mod, cidPtr, cidLen)
	if !ok {
		return 0
	}
	topic, ok := e.executor.ReadFromGuest(mod, topicPtr, topicLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.WSPubSubBridge(ctx, string(cid), string(topic)); err != nil {
		e.logger.Warn("ws_pubsub_bridge failed",
			zap.String("client_id", string(cid)),
			zap.String("topic", string(topic)),
			zap.Error(err))
		return 0
	}
	return 1
}

// hWSPubSubUnbridge is the WASM-callable wrapper for WSPubSubUnbridge.
func (e *Engine) hWSPubSubUnbridge(ctx context.Context, mod api.Module,
	cidPtr, cidLen, topicPtr, topicLen uint32) uint32 {
	cid, ok := e.executor.ReadFromGuest(mod, cidPtr, cidLen)
	if !ok {
		return 0
	}
	topic, ok := e.executor.ReadFromGuest(mod, topicPtr, topicLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.WSPubSubUnbridge(ctx, string(cid), string(topic)); err != nil {
		e.logger.Warn("ws_pubsub_unbridge failed",
			zap.String("client_id", string(cid)),
			zap.String("topic", string(topic)),
			zap.Error(err))
		return 0
	}
	return 1
}

// hFunctionInvoke is the WASM-callable wrapper for FunctionInvoke. Used by
// the rpc-router persistent function (and any future dispatcher) to run
// another function in the same namespace synchronously and forward its
// output back to its caller.
//
// Inputs:
//
//	namePtr/nameLen       — UTF-8 target function name
//	payloadPtr/payloadLen — raw input bytes for the target function
//
// Returns a packed uint64 (ptr<<32 | len) pointing to the target's output
// bytes in guest memory, or 0 on error. The caller is expected to JSON-
// decode the output (target functions ack with JSON envelopes).
func (e *Engine) hFunctionInvoke(ctx context.Context, mod api.Module,
	namePtr, nameLen, payloadPtr, payloadLen uint32) uint64 {
	name, ok := e.executor.ReadFromGuest(mod, namePtr, nameLen)
	if !ok {
		return 0
	}
	payload, ok := e.executor.ReadFromGuest(mod, payloadPtr, payloadLen)
	if !ok {
		return 0
	}
	out, err := e.hostServices.FunctionInvoke(ctx, string(name), payload)
	if err != nil {
		e.logger.Warn("function_invoke failed",
			zap.String("name", string(name)),
			zap.Error(err))
		return 0
	}
	return e.executor.WriteToGuest(ctx, mod, out)
}

// hWSSend is the WASM-callable wrapper for WSSend.
// Inputs: clientID + raw frame bytes. clientID may be empty — in that case
// the host falls back to the current invocation's WS client (if any).
// Returns 1 on success, 0 on error.
func (e *Engine) hWSSend(ctx context.Context, mod api.Module,
	cidPtr, cidLen, dataPtr, dataLen uint32) uint32 {
	cid, ok := e.executor.ReadFromGuest(mod, cidPtr, cidLen)
	if !ok {
		return 0
	}
	data, ok := e.executor.ReadFromGuest(mod, dataPtr, dataLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.WSSend(ctx, string(cid), data); err != nil {
		e.logger.Warn("ws_send failed",
			zap.String("client_id", string(cid)),
			zap.Error(err))
		return 0
	}
	return 1
}

// hWSBroadcast is the WASM-callable wrapper for WSBroadcast.
// Inputs: topic + raw frame bytes. Sends `data` to every WS client currently
// subscribed to `topic` in the function's namespace. Returns 1 on success,
// 0 on error.
func (e *Engine) hWSBroadcast(ctx context.Context, mod api.Module,
	topicPtr, topicLen, dataPtr, dataLen uint32) uint32 {
	topic, ok := e.executor.ReadFromGuest(mod, topicPtr, topicLen)
	if !ok {
		return 0
	}
	data, ok := e.executor.ReadFromGuest(mod, dataPtr, dataLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.WSBroadcast(ctx, string(topic), data); err != nil {
		e.logger.Warn("ws_broadcast failed",
			zap.String("topic", string(topic)),
			zap.Error(err))
		return 0
	}
	return 1
}

// hPushSend is the WASM-callable wrapper for PushSend.
// Inputs:
//   userIDPtr/userIDLen — UTF-8 user ID to push to (within the function's
//                         own namespace; the namespace is server-side trusted)
//   msgPtr/msgLen       — JSON payload matching hostfunctions.PushSendArgs
// Returns 1 on success, 0 on error.
func (e *Engine) hPushSend(ctx context.Context, mod api.Module,
	userIDPtr, userIDLen, msgPtr, msgLen uint32) uint32 {
	userID, ok := e.executor.ReadFromGuest(mod, userIDPtr, userIDLen)
	if !ok {
		return 0
	}
	msgJSON, ok := e.executor.ReadFromGuest(mod, msgPtr, msgLen)
	if !ok {
		return 0
	}
	if err := e.hostServices.PushSend(ctx, string(userID), msgJSON); err != nil {
		e.logger.Error("host function push_send failed",
			zap.String("user_id", string(userID)),
			zap.Error(err))
		return 0
	}
	return 1
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
