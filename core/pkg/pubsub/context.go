package pubsub

import "context"

// Context utilities for namespace override
// Keep type unexported and expose the key as exported constant to avoid collisions
// while still allowing other packages to use the exact key value.
type ctxKey string

// CtxKeyNamespaceOverride is the context key used to override namespace per pubsub call
const CtxKeyNamespaceOverride ctxKey = "pubsub_ns_override"

// WithNamespace returns a new context that carries a pubsub namespace override
func WithNamespace(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, CtxKeyNamespaceOverride, ns)
}
