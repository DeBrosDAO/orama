package hostfunctions

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/serverless/triggers"
)

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
		Namespace:        cur.Namespace,
		FunctionName:     name,
		Input:            payload,
		TriggerType:      serverless.TriggerTypeWebSocket,
		CallerWallet:     cur.CallerWallet,
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
