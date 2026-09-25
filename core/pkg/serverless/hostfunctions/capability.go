package hostfunctions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// Capabilities (feat-264): a function running for a device mints the token a
// correspondent opens this function's WebSocket with, and a function opened
// with one reads what it grants.

var (
	errNoCapabilityIssuer = errors.New("this gateway cannot mint or revoke capabilities: it has no cluster secret")
	errNoInvocation       = errors.New("no invocation: capabilities are handled from inside a function")
)

// SetCapabilityIssuer wires what mints and revokes capabilities. Called once at
// gateway start, before any function runs.
func (h *HostFunctions) SetCapabilityIssuer(issuer serverless.CapabilityIssuer) {
	h.capabilityIssuer = issuer
}

// MintCapability issues a capability for the calling function's WebSocket,
// naming resource, for ttl, issued by the device the caller's session is bound
// to. It returns the capability as JSON:
// {"token","cap_id","resource","issuer_device","expires_at"}.
//
// The function it opens is the one minting it, never another: a function hands
// out access to itself. A caller whose session is bound to no device — or a
// socket opened with a capability, which has no session at all — cannot mint
// one, because a capability is revoked with the device that issued it.
func (h *HostFunctions) MintCapability(ctx context.Context, resource string, ttl time.Duration) (string, error) {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return "", capabilityErr("capability_mint", errNoInvocation)
	}
	if h.capabilityIssuer == nil {
		return "", capabilityErr("capability_mint", errNoCapabilityIssuer)
	}
	grant, token, err := h.capabilityIssuer.Mint(ctx, cur.Namespace, cur.FunctionName, resource, cur.CallerDeviceID, ttl)
	if err != nil {
		return "", capabilityErr("capability_mint", err)
	}
	out, err := json.Marshal(struct {
		Token string `json:"token"`
		*serverless.CapabilityGrant
	}{token, grant})
	if err != nil {
		return "", capabilityErr("capability_mint", fmt.Errorf("encode the capability: %w", err))
	}
	return string(out), nil
}

// RevokeCapability refuses one capability of the calling function's namespace
// from now on, named by the token capability_mint returned; the gateway's
// sweeper closes the sockets opened with it.
func (h *HostFunctions) RevokeCapability(ctx context.Context, token string) error {
	cur := h.currentInvocationContext(ctx)
	if cur == nil {
		return capabilityErr("capability_revoke", errNoInvocation)
	}
	if h.capabilityIssuer == nil {
		return capabilityErr("capability_revoke", errNoCapabilityIssuer)
	}
	if err := h.capabilityIssuer.Revoke(ctx, cur.Namespace, token); err != nil {
		return capabilityErr("capability_revoke", err)
	}
	return nil
}

// GetCallerCapability returns what the capability the caller's socket was
// opened with grants, as JSON {"cap_id","resource","issuer_device","expires_at"},
// or "" when the caller came in on a credential.
func (h *HostFunctions) GetCallerCapability(ctx context.Context) string {
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.CallerCapability == nil {
		return ""
	}
	out, err := json.Marshal(cur.CallerCapability)
	if err != nil {
		if h.logger != nil {
			h.logger.Error("could not encode the caller's capability", zap.Error(err))
		}
		return ""
	}
	return string(out)
}

func capabilityErr(fn string, cause error) error {
	return &serverless.HostFunctionError{Function: fn, Cause: cause}
}
