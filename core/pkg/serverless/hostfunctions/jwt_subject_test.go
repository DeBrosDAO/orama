package hostfunctions

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Bug #215: get_caller_jwt_subject must return the JWT `sub` exposed
// in the InvocationContext, independent of the caller-wallet field.

func TestGetCallerJWTSubject_returns_sub_when_set(t *testing.T) {
	h := &HostFunctions{}
	invCtx := invocationCtx(&serverless.InvocationContext{
		// CallerWallet is the namespace pseudo-wallet (resolved via API key);
		// CallerJWTSubject is the actual signing wallet from the Bearer JWT.
		CallerWallet:     "anchat-test",
		CallerJWTSubject: "A3ZGpMKPtsmYVtXr6Gnf5u4x6j4dgZWnpyXXFAiCibCC",
	})

	if got := h.GetCallerJWTSubject(invCtx); got != "A3ZGpMKPtsmYVtXr6Gnf5u4x6j4dgZWnpyXXFAiCibCC" {
		t.Errorf("GetCallerJWTSubject = %q, want the JWT subject (not the namespace)", got)
	}
	// And GetCallerWallet still returns the resolved-wallet (namespace in
	// this case) — they're independent accessors by design.
	if got := h.GetCallerWallet(invCtx); got != "anchat-test" {
		t.Errorf("GetCallerWallet = %q, want anchat-test (the resolved caller-wallet)", got)
	}
}

func TestGetCallerJWTSubject_empty_when_not_jwt_authed(t *testing.T) {
	h := &HostFunctions{}
	invCtx := invocationCtx(&serverless.InvocationContext{
		CallerWallet:     "anchat-test",
		CallerJWTSubject: "", // request was API-key only
	})

	if got := h.GetCallerJWTSubject(invCtx); got != "" {
		t.Errorf("GetCallerJWTSubject = %q, want empty for API-key-only auth", got)
	}
}

func TestGetCallerJWTSubject_empty_when_no_invocation_context(t *testing.T) {
	h := &HostFunctions{}
	if got := h.GetCallerJWTSubject(context.Background()); got != "" {
		t.Errorf("GetCallerJWTSubject = %q, want empty when no inv ctx", got)
	}
}
