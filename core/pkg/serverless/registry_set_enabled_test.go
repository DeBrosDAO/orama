package serverless

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// Plan 11.5 — disable/enable function status toggle.
//
// SetEnabled is the runtime control surface operators use during
// incident response to pause a misbehaving function without
// redeploying. The Invoker treats inactive functions as missing, so
// new invocations get 404; in-flight ones finish normally.
//
// These tests pin the validation semantics. The actual UPDATE path
// requires rqlite (covered by the registry/function_store integration
// tests once added).

func TestRegistry_SetEnabled_emptyNamespaceRejected(t *testing.T) {
	r := &Registry{logger: zap.NewNop()}
	err := r.SetEnabled(context.Background(), "", "fn-1", true)
	if err == nil {
		t.Fatal("empty namespace must be rejected (defense at boundary)")
	}
}

func TestRegistry_SetEnabled_emptyNameRejected(t *testing.T) {
	r := &Registry{logger: zap.NewNop()}
	err := r.SetEnabled(context.Background(), "ns", "", true)
	if err == nil {
		t.Fatal("empty name must be rejected (defense at boundary)")
	}
}

func TestRegistry_SetEnabled_trimsWhitespace(t *testing.T) {
	// Whitespace-only inputs should also be rejected — strings.TrimSpace
	// makes "   " collapse to "" which the empty-check then catches.
	// Without this, a caller passing "  " would slip through and bind a
	// degenerate row update.
	r := &Registry{logger: zap.NewNop()}
	if err := r.SetEnabled(context.Background(), "   ", "name", true); err == nil {
		t.Error("whitespace-only namespace must be rejected")
	}
	if err := r.SetEnabled(context.Background(), "ns", "   ", true); err == nil {
		t.Error("whitespace-only name must be rejected")
	}
}
