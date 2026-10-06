package hostfunctions

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// currentInvocationContext returns the InvocationContext this host call is
// part of, or nil when it is part of none.
//
// It reads the context and nothing else. There used to be a fallback to a
// field on the shared HostFunctions, set before each stateless execution and
// cleared after — which two concurrent invocations overwrote for each other.
// The loser read a cross-tenant namespace, or nil. A host call outside an
// invocation now returns nil here and is refused by its caller, rather than
// being served with whoever ran last.
func (h *HostFunctions) currentInvocationContext(ctx context.Context) *serverless.InvocationContext {
	return serverless.InvocationContextFromCtx(ctx)
}
