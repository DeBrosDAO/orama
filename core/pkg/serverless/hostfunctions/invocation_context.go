package hostfunctions

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// currentInvocationContext returns the active InvocationContext for a host
// call. ctx-attached values (via serverless.WithInvocationContext) take
// precedence over the singleton field — see the comment on
// serverless.WithInvocationContext for the cross-tenant identity-leak
// rationale.
//
// Returns nil if neither source has a context (e.g. a host call made
// outside any invocation, which generally indicates a bug in wiring).
func (h *HostFunctions) currentInvocationContext(ctx context.Context) *serverless.InvocationContext {
	if c := serverless.InvocationContextFromCtx(ctx); c != nil {
		return c
	}
	h.invCtxLock.RLock()
	defer h.invCtxLock.RUnlock()
	return h.invCtx
}
