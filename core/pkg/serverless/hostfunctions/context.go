package hostfunctions

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// SetInvocationContext sets the current invocation context.
// Must be called before executing a function.
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

// ClearContext clears the invocation context after execution.
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

// FunctionInvoke synchronously runs another function in the same namespace
// and returns its output bytes. Caller wallet, JWT claims, and WS client
// ID are inherited from the current invocation so the inner function sees
// the same authenticated identity. Returns ErrFunctionInvokeNotAvailable
// when no invoker has been wired (e.g. tests).
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

	h.invCtxLock.RLock()
	cur := h.invCtx
	h.invCtxLock.RUnlock()
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
		CallerIP:     cur.CallerIP,
		WSClientID:   cur.WSClientID,
		CallerClaims: cur.CallerClaims,
	}
	resp, err := inv.Invoke(ctx, req)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "function_invoke", Cause: err}
	}
	return resp.Output, nil
}

// GetEnv retrieves an environment variable for the function.
func (h *HostFunctions) GetEnv(ctx context.Context, key string) (string, error) {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil || h.invCtx.EnvVars == nil {
		return "", nil
	}

	return h.invCtx.EnvVars[key], nil
}

// GetSecret retrieves a decrypted secret.
func (h *HostFunctions) GetSecret(ctx context.Context, name string) (string, error) {
	if h.secrets == nil {
		return "", &serverless.HostFunctionError{Function: "get_secret", Cause: serverless.ErrDatabaseUnavailable}
	}

	h.invCtxLock.RLock()
	namespace := ""
	if h.invCtx != nil {
		namespace = h.invCtx.Namespace
	}
	h.invCtxLock.RUnlock()

	value, err := h.secrets.Get(ctx, namespace, name)
	if err != nil {
		return "", &serverless.HostFunctionError{Function: "get_secret", Cause: err}
	}

	return value, nil
}

// GetRequestID returns the current request ID.
func (h *HostFunctions) GetRequestID(ctx context.Context) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil {
		return ""
	}
	return h.invCtx.RequestID
}

// GetCallerWallet returns the wallet address of the caller.
func (h *HostFunctions) GetCallerWallet(ctx context.Context) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil {
		return ""
	}
	return h.invCtx.CallerWallet
}

// GetWSClientID returns the WebSocket client ID for the current invocation,
// or empty string if the function wasn't invoked via a WS connection.
func (h *HostFunctions) GetWSClientID(ctx context.Context) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil {
		return ""
	}
	return h.invCtx.WSClientID
}

// GetCallerClaim returns the value of a custom JWT claim for the caller, or
// empty string if the claim is missing or the request was not JWT-authenticated.
//
// "Custom" here means claims set on JWTClaims.Custom by the auth service —
// standard claims (sub, namespace, etc.) have dedicated accessors.
func (h *HostFunctions) GetCallerClaim(ctx context.Context, name string) string {
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()

	if h.invCtx == nil || h.invCtx.CallerClaims == nil {
		return ""
	}
	return h.invCtx.CallerClaims[name]
}
