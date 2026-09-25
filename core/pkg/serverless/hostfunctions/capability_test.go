package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// A function mints capabilities for its own socket, issued by the device its
// caller's session is bound to; it reads the capability its own socket was
// opened with.

type recordingIssuer struct {
	namespace, function, resource, device, revoked string
	ttl                                            time.Duration
	err                                            error
}

func (r *recordingIssuer) Mint(_ context.Context, namespace, function, resource, device string, ttl time.Duration) (*serverless.CapabilityGrant, string, error) {
	r.namespace, r.function, r.resource, r.device, r.ttl = namespace, function, resource, device, ttl
	if r.err != nil {
		return nil, "", r.err
	}
	return &serverless.CapabilityGrant{ID: "cap-1", Resource: resource, IssuerDevice: device, ExpiresAt: 42}, "token", nil
}

func (r *recordingIssuer) Revoke(_ context.Context, namespace, id string) error {
	r.namespace, r.revoked = namespace, id
	return r.err
}

func TestMintCapability_opensTheMintingFunctionOnly(t *testing.T) {
	issuer := &recordingIssuer{}
	h := &HostFunctions{}
	h.SetCapabilityIssuer(issuer)
	ctx := invocationCtx(&serverless.InvocationContext{
		Namespace: "anchat", FunctionName: "rpc-router", CallerJWTSubject: "0xwallet", CallerDeviceID: "device-1",
	})

	out, err := h.MintCapability(ctx, "mailbox-7", time.Hour)
	if err != nil {
		t.Fatalf("MintCapability: %v", err)
	}
	if issuer.namespace != "anchat" || issuer.function != "rpc-router" || issuer.device != "device-1" || issuer.ttl != time.Hour {
		t.Errorf("minted with %+v", issuer)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["token"] != "token" || got["cap_id"] != "cap-1" {
		t.Errorf("MintCapability returned %s (%v)", out, err)
	}
}

func TestMintCapability_refusedWithoutAnIssuerOrAnInvocation(t *testing.T) {
	h := &HostFunctions{}
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "anchat", FunctionName: "fn", CallerDeviceID: "d"})
	if _, err := h.MintCapability(ctx, "r", time.Hour); err == nil {
		t.Error("minted with no issuer wired")
	}
	h.SetCapabilityIssuer(&recordingIssuer{})
	if _, err := h.MintCapability(context.Background(), "r", time.Hour); err == nil {
		t.Error("minted outside an invocation")
	}
	failing := &recordingIssuer{err: errors.New("no device")}
	h.SetCapabilityIssuer(failing)
	if _, err := h.MintCapability(ctx, "r", time.Hour); err == nil {
		t.Error("the issuer's refusal was not passed on")
	}
}

func TestRevokeCapability_revokesInTheCallersNamespace(t *testing.T) {
	issuer := &recordingIssuer{}
	h := &HostFunctions{}
	h.SetCapabilityIssuer(issuer)
	ctx := invocationCtx(&serverless.InvocationContext{Namespace: "anchat", FunctionName: "fn"})
	if err := h.RevokeCapability(ctx, "cap-1"); err != nil || issuer.namespace != "anchat" || issuer.revoked != "cap-1" {
		t.Errorf("RevokeCapability: %v, %+v", err, issuer)
	}
}

func TestGetCallerCapability(t *testing.T) {
	h := &HostFunctions{}
	grant := &serverless.CapabilityGrant{ID: "cap-1", Resource: "mailbox-7", IssuerDevice: "device-1", ExpiresAt: 42}
	out := h.GetCallerCapability(invocationCtx(&serverless.InvocationContext{CallerCapability: grant}))
	var got serverless.CapabilityGrant
	if err := json.Unmarshal([]byte(out), &got); err != nil || got != *grant {
		t.Errorf("GetCallerCapability = %s (%v)", out, err)
	}
	if out := h.GetCallerCapability(invocationCtx(&serverless.InvocationContext{CallerJWTSubject: "0xw"})); out != "" {
		t.Errorf("a credential caller reported capability %q", out)
	}
}

// A capability opens the function that minted it, no other: a nested call
// from a capability socket does not carry it.
func TestFunctionInvoke_doesNotCarryTheCapability(t *testing.T) {
	inv := &nestedRecordingInvoker{}
	h := hostWithInvoker(inv)
	ctx := parentCtx(&serverless.InvocationContext{
		Namespace:        "anchat",
		TriggerType:      serverless.TriggerTypeWebSocket,
		CallerCapability: &serverless.CapabilityGrant{ID: "cap-1"},
	})
	if _, err := h.FunctionInvoke(ctx, "callee", []byte(`{}`)); err != nil {
		t.Fatalf("FunctionInvoke: %v", err)
	}
	if got := inv.last().CallerCapability; got != nil {
		t.Errorf("the nested call carried capability %+v", got)
	}
}
