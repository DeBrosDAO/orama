package hostfunctions

import (
	"context"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
	"go.uber.org/zap"
)

// asyncInvokeMaxInFlight bounds concurrently-running FunctionInvokeAsync
// goroutines across the whole gateway. Each async invocation still passes
// through the engine's own execution semaphore; this cap bounds the GOROUTINES
// so a client flooding WS frames can't spawn an unbounded number of pending
// invocations. When hit, FunctionInvokeAsync rejects so the guest applies
// backpressure (e.g. falls back to a synchronous invoke or returns "busy").
const asyncInvokeMaxInFlight = 256

// asyncInvokeTimeout bounds a single async invocation. Detached from the frame
// ctx (which is cancelled when ws_frame returns), so it carries its own
// deadline — generous enough for cross-region work, tight enough that a stuck
// invocation eventually frees its in-flight slot.
const asyncInvokeTimeout = 30 * time.Second

// SetInvocationContext sets the current invocation context on the
// singleton field. STATELESS execution path uses this (paired with
// ClearContext) for per-call binding via the executor's setter/clearer
// hook. PERSISTENT WS uses ctx-propagation instead — see
// invocation_context.go for the cross-tenant race rationale.
func (h *HostFunctions) SetInvocationContext(invCtx *serverless.InvocationContext) {
	h.invCtxLock.Lock()
	defer h.invCtxLock.Unlock()
	h.invCtx = invCtx
	h.logs = make([]serverless.LogEntry, 0) // Reset logs for new invocation
}

// GetLogs returns the captured logs for the current invocation.
func (h *HostFunctions) GetLogs() []serverless.LogEntry {
	h.logsLock.Lock()
	defer h.logsLock.Unlock()
	logsCopy := make([]serverless.LogEntry, len(h.logs))
	copy(logsCopy, h.logs)
	return logsCopy
}

// ClearContext clears the singleton invocation context after stateless
// execution. No-op effect for persistent WS (which never uses the
// singleton field).
func (h *HostFunctions) ClearContext() {
	h.invCtxLock.Lock()
	defer h.invCtxLock.Unlock()
	h.invCtx = nil
}

// SetInvoker wires the function invoker used by FunctionInvoke. Must be
// called once after both HostFunctions and Invoker exist (Invoker depends
// on HostServices, so the cycle is broken via this setter rather than a
// constructor argument).
func (h *HostFunctions) SetInvoker(inv serverless.FunctionInvoker) {
	h.invokerLock.Lock()
	defer h.invokerLock.Unlock()
	h.invoker = inv
}

// SetTriggerDispatcher wires the PubSubDispatcher used by PubSubPublish /
// PubSubPublishBatch to synchronously fire wildcard triggers for
// WASM-published topics on this gateway (bugboard #93). nil disables
// local wildcard dispatch — only libp2p subscribe delivery applies, and
// wildcard triggers will be silent for WASM publishes.
//
// Wired after both HostFunctions and PubSubDispatcher exist; the
// dispatcher depends on the engine (which depends on HostFunctions), so
// the cycle is broken via this setter — same pattern as SetInvoker.
func (h *HostFunctions) SetTriggerDispatcher(d *triggers.PubSubDispatcher) {
	h.triggerDispatcherLock.Lock()
	defer h.triggerDispatcherLock.Unlock()
	h.triggerDispatcher = d
}

// FunctionInvoke synchronously runs another function in the same namespace
// and returns its output bytes. Caller wallet, JWT claims, and WS client
// ID are inherited from the current invocation so the inner function sees
// the same authenticated identity. Returns ErrFunctionInvokeNotAvailable
// when no invoker has been wired (e.g. tests).
//
// Identity propagation: ctx-attached invCtx wins over the singleton —
// this is what makes persistent WS function_invoke calls race-free across
// concurrent connections (see invocation_context.go).
func (h *HostFunctions) FunctionInvoke(ctx context.Context, name string, payload []byte) ([]byte, error) {
	h.invokerLock.RLock()
	inv := h.invoker
	h.invokerLock.RUnlock()
	if inv == nil {
		return nil, &serverless.HostFunctionError{
			Function: "function_invoke",
			Cause:    serverless.ErrFunctionInvokeNotAvailable,
		}
	}

	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return nil, &serverless.HostFunctionError{
			Function: "function_invoke",
			Cause:    serverless.ErrFunctionInvokeNotAvailable,
		}
	}

	req := &serverless.InvokeRequest{
		Namespace:    cur.Namespace,
		FunctionName: name,
		Input:        payload,
		TriggerType:  serverless.TriggerTypeWebSocket,
		CallerWallet: cur.CallerWallet,
		// Inherit the parent's admin bit so an internal→internal call works
		// while external→internal stays blocked (bugboard #152). When the
		// parent context never carried admin (external caller), this is false.
		CallerIsAdmin:    cur.CallerIsAdmin,
		CallerIP:         cur.CallerIP,
		WSClientID:       cur.WSClientID,
		CallerClaims:     cur.CallerClaims,
		CallerJWTSubject: cur.CallerJWTSubject,
		// Propagate trigger depth so a wildcard-triggered handler that
		// calls function_invoke(B) — and B then publishes a topic that
		// matches A's own wildcard — still hits the maxTriggerDepth
		// guard. Without this, depth resets to 0 on every
		// function_invoke hop and the recursion bound reopens.
		// Bugboard #93 follow-up (audit C7).
		TriggerDepth: cur.TriggerDepth,
	}
	resp, err := inv.Invoke(ctx, req)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "function_invoke", Cause: err}
	}
	return resp.Output, nil
}

// FunctionInvokeAsync runs another function in the same namespace CONCURRENTLY
// and returns immediately, WITHOUT blocking the caller or returning the
// target's output. It exists so a persistent dispatcher (rpc-router) — a
// single stateful instance that must process frames serially — can fan out
// slow per-RPC handlers without freezing its frame loop for the full
// (cross-region) duration of each one. The handlers run in the engine's
// execution pool and deliver their own results to the client via ws_send
// (they inherit the same WS client ID).
//
// The target inherits the caller's identity exactly like FunctionInvoke.
// Returns an error only when the call can't be ACCEPTED: no invoker wired, no
// invocation context, or the in-flight cap is reached (backpressure). Failures
// INSIDE the target are not reported here — they surface via the target's own
// logging / ws_send, because the caller has already moved on.
func (h *HostFunctions) FunctionInvokeAsync(ctx context.Context, name string, payload []byte) error {
	h.invokerLock.RLock()
	inv := h.invoker
	h.invokerLock.RUnlock()
	if inv == nil {
		return &serverless.HostFunctionError{
			Function: "function_invoke_async",
			Cause:    serverless.ErrFunctionInvokeNotAvailable,
		}
	}

	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return &serverless.HostFunctionError{
			Function: "function_invoke_async",
			Cause:    serverless.ErrFunctionInvokeNotAvailable,
		}
	}

	// Bound in-flight goroutines. nil sem = bare test construction → unbounded
	// (production always builds it in NewHostFunctions). A full channel means
	// we're saturated; reject so the guest applies backpressure.
	if h.asyncInvokeSem != nil {
		select {
		case h.asyncInvokeSem <- struct{}{}:
		default:
			return &serverless.HostFunctionError{
				Function: "function_invoke_async",
				Cause:    fmt.Errorf("too many in-flight async invocations (max %d)", asyncInvokeMaxInFlight),
			}
		}
	}

	// Copy identity AND payload before returning: the invocation context can
	// be swapped (auth.refresh) and `payload` is a VIEW into guest memory that
	// the next frame may overwrite — the goroutine outlives this call, so it
	// must own its inputs.
	//
	// The struct copy is shallow: snapshot.CallerClaims / EnvVars share the
	// source maps. That is safe because an InvocationContext's maps are
	// immutable after construction (auth.refresh swaps the whole pointer via
	// UpdateInvocationContext rather than mutating in place); no code writes
	// these maps on a live context. Keep that invariant if you touch the
	// refresh path, or clone the maps here.
	snapshot := *cur
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)
	logger := h.logger

	go func() {
		if h.asyncInvokeSem != nil {
			defer func() { <-h.asyncInvokeSem }()
		}
		bgCtx := serverless.WithInvocationContext(context.Background(), &snapshot)
		bgCtx, cancel := context.WithTimeout(bgCtx, asyncInvokeTimeout)
		defer cancel()

		req := &serverless.InvokeRequest{
			Namespace:    snapshot.Namespace,
			FunctionName: name,
			Input:        payloadCopy,
			TriggerType:  serverless.TriggerTypeWebSocket,
			CallerWallet: snapshot.CallerWallet,
			// Inherit the parent's admin bit (bugboard #152): internal→internal
			// async calls work; external→internal stay blocked.
			CallerIsAdmin:    snapshot.CallerIsAdmin,
			CallerIP:         snapshot.CallerIP,
			WSClientID:       snapshot.WSClientID,
			CallerClaims:     snapshot.CallerClaims,
			CallerJWTSubject: snapshot.CallerJWTSubject,
			TriggerDepth:     snapshot.TriggerDepth,
		}
		if _, err := inv.Invoke(bgCtx, req); err != nil && logger != nil {
			logger.Warn("function_invoke_async target failed",
				zap.String("name", name),
				zap.String("namespace", snapshot.Namespace),
				zap.Error(err))
		}
	}()
	return nil
}

// GetEnv retrieves an environment variable for the function.
func (h *HostFunctions) GetEnv(ctx context.Context, key string) (string, error) {
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.EnvVars == nil {
		return "", nil
	}
	return cur.EnvVars[key], nil
}

// GetSecret retrieves a decrypted secret.
func (h *HostFunctions) GetSecret(ctx context.Context, name string) (string, error) {
	if h.secrets == nil {
		return "", &serverless.HostFunctionError{Function: "get_secret", Cause: serverless.ErrDatabaseUnavailable}
	}

	namespace := ""
	if cur := h.currentInvocationContext(ctx); cur != nil {
		namespace = cur.Namespace
	}

	value, err := h.secrets.Get(ctx, namespace, name)
	if err != nil {
		return "", &serverless.HostFunctionError{Function: "get_secret", Cause: err}
	}

	return value, nil
}

// GetRequestID returns the current request ID.
func (h *HostFunctions) GetRequestID(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return ""
	}
	return cur.RequestID
}

// GetCallerWallet returns the wallet address of the caller.
func (h *HostFunctions) GetCallerWallet(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return ""
	}
	return cur.CallerWallet
}

// GetWSClientID returns the WebSocket client ID for the current invocation,
// or empty string if the function wasn't invoked via a WS connection.
func (h *HostFunctions) GetWSClientID(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return ""
	}
	return cur.WSClientID
}

// GetCallerClaim returns the value of a custom JWT claim for the caller, or
// empty string if the claim is missing or the request was not JWT-authenticated.
//
// "Custom" here means claims set on JWTClaims.Custom by the auth service —
// standard claims (sub, namespace, etc.) have dedicated accessors.
func (h *HostFunctions) GetCallerClaim(ctx context.Context, name string) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.CallerClaims == nil {
		return ""
	}
	return cur.CallerClaims[name]
}

// GetCallerJWTSubject returns the JWT `sub` claim explicitly, independent
// of the API-key-vs-JWT precedence used by GetCallerWallet. Empty when the
// request was not JWT-authenticated. Bug #215.
//
// Use this when a function MUST bind on the JWT-signed identity (e.g. a
// signup flow that verifies the wallet the caller is registering matches
// the wallet that signed the auth challenge). GetCallerWallet may return
// the namespace pseudo-identifier if the caller also presents an API key.
func (h *HostFunctions) GetCallerJWTSubject(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return ""
	}
	return cur.CallerJWTSubject
}
