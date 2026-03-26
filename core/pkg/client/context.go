package client

import (
	"context"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// contextKey for internal operations
type contextKey string

const (
	// ctxKeyInternal marks contexts for internal system operations that bypass auth
	ctxKeyInternal contextKey = "internal_operation"
)

// WithNamespace applies pubsub namespace override to the context.
// It is a convenience helper for client callers to ensure subsystems receive
// the same, consistent namespace override.
func WithNamespace(ctx context.Context, ns string) context.Context {
	ctx = pubsub.WithNamespace(ctx, ns)
	return ctx
}

// WithInternalAuth creates a context that bypasses authentication for internal system operations.
// This should only be used by the system itself (migrations, internal tasks, etc.)
func WithInternalAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyInternal, true)
}

// IsInternalContext checks if a context is marked for internal operations
func IsInternalContext(ctx context.Context) bool {
	if v := ctx.Value(ctxKeyInternal); v != nil {
		if internal, ok := v.(bool); ok {
			return internal
		}
	}
	return false
}
