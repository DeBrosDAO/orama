package hostfunctions

import (
	"context"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

func TestGetWSClientID_unset_returns_empty(t *testing.T) {
	h := &HostFunctions{}
	if got := h.GetWSClientID(context.Background()); got != "" {
		t.Errorf("expected empty WSClientID, got %q", got)
	}
}

func TestGetWSClientID_set_returns_value(t *testing.T) {
	h := &HostFunctions{}
	h.SetInvocationContext(&serverless.InvocationContext{
		WSClientID: "client-abc",
	})
	if got := h.GetWSClientID(context.Background()); got != "client-abc" {
		t.Errorf("expected 'client-abc', got %q", got)
	}
}

func TestGetCallerClaim_no_claims_returns_empty(t *testing.T) {
	h := &HostFunctions{}
	h.SetInvocationContext(&serverless.InvocationContext{})
	if got := h.GetCallerClaim(context.Background(), "tier"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestGetCallerClaim_present(t *testing.T) {
	h := &HostFunctions{}
	h.SetInvocationContext(&serverless.InvocationContext{
		CallerClaims: map[string]string{
			"tier":         "premium",
			"subscription": "active",
		},
	})
	if got := h.GetCallerClaim(context.Background(), "tier"); got != "premium" {
		t.Errorf("expected 'premium', got %q", got)
	}
	if got := h.GetCallerClaim(context.Background(), "subscription"); got != "active" {
		t.Errorf("expected 'active', got %q", got)
	}
	if got := h.GetCallerClaim(context.Background(), "missing"); got != "" {
		t.Errorf("expected empty for missing claim, got %q", got)
	}
}

func TestGetCallerClaim_no_invctx_returns_empty(t *testing.T) {
	h := &HostFunctions{}
	if got := h.GetCallerClaim(context.Background(), "tier"); got != "" {
		t.Errorf("expected empty when invCtx is nil, got %q", got)
	}
}
