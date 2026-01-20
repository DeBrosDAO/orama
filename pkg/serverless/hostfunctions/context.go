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
