package serverless

import "context"

// invCtxKey is the unexported context-value key used to attach an
// InvocationContext to a Go context. The empty struct is the standard
// Go pattern for context keys (avoids string-collision risk).
type invCtxKey struct{}

// WithInvocationContext returns a derived ctx that carries invCtx. Host
// function accessors check the ctx FIRST and only fall back to the
// HostFunctions singleton field when nothing is carried on ctx.
//
// Why this exists: HostFunctions is a process-wide singleton (one per
// gateway engine). Its `invCtx` field is shared across all WASM instances.
// For STATELESS functions the gateway sets/clears that field per-call
// (executor contextSetter/contextClearer), but the lock is released
// before WASM runs — two concurrent invocations CAN race on the field,
// and one's host call CAN read the other's identity.
//
// For PERSISTENT WS functions the race is far worse: the field used to be
// bound ONCE at instantiation and reused for the connection's lifetime.
// Two simultaneous persistent WS connections from different users
// overwrote each other's invCtx, and every subsequent function_invoke /
// GetCallerJWTSubject / GetSecret call from inside the WASM read whatever
// was bound LAST — silently leaking identity across tenants.
//
// The fix is per-call invCtx propagation through Go's context.Context.
// wazero passes the ctx given to api.Function.Call all the way through
// to host function callbacks (engine.go's host-function wrappers receive
// it), so every WASM-host hop carries its own invCtx and never reads the
// shared field.
//
// Persistent WS uses this exclusively (see persistent.Instance, which
// wraps every export call's ctx with the per-instance invCtx).
//
// Stateless Engine.Execute also attaches invCtx via this helper since
// bugboard #348 — AnChat's pubsub-triggered message-push-handler
// confirmed the "microseconds" race window was actually observable
// under production fan-out load: concurrent invocations either
// cross-tenant-leaked the namespace (silent) or saw a nil singleton
// during the brief window between contextSetter on one goroutine and
// contextClearer on another, producing "no namespace in invocation
// context" errors at host-fn entry. The singleton SetInvocationContext
// path remains in place as defense-in-depth — every host fn resolves
// via currentInvocationContext, which prefers ctx-attached over the
// singleton field, so the race is closed for the live path.
func WithInvocationContext(ctx context.Context, invCtx *InvocationContext) context.Context {
	if invCtx == nil {
		return ctx
	}
	return context.WithValue(ctx, invCtxKey{}, invCtx)
}

// InvocationContextFromCtx extracts the invCtx attached via
// WithInvocationContext, or nil if none is present. Exported so the
// hostfunctions package and any other consumer can read it without
// duplicating the key type.
func InvocationContextFromCtx(ctx context.Context) *InvocationContext {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(invCtxKey{}).(*InvocationContext)
	return v
}
