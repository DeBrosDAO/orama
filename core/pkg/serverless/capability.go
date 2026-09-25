package serverless

import (
	"context"
	"fmt"
	"time"
)

// Capabilities (feat-264): a function's WebSocket opened on a token one of the
// namespace's devices minted through the function, rather than on the caller's
// credential. The gateway mints and checks them; this package carries what
// they grant to the function.

// WSAuthCapability is the ws_auth value that lets a function's WebSocket be
// opened with a capability as well as with a credential.
const WSAuthCapability = "capability"

// ValidateWSAuth refuses a ws_auth this gateway does not know. A typo must not
// deploy as "credential only" and leave the application wondering why its
// capabilities are refused.
func ValidateWSAuth(v string) error {
	switch v {
	case "", WSAuthCapability:
		return nil
	}
	return &ValidationError{Field: "ws_auth", Message: fmt.Sprintf("must be empty or %q (got %q)", WSAuthCapability, v)}
}

// CapabilityGrant is what a capability grants the socket opened with it. The
// function reads it with get_caller_capability.
type CapabilityGrant struct {
	ID           string `json:"cap_id"`
	Resource     string `json:"resource"`
	IssuerDevice string `json:"issuer_device"`
	ExpiresAt    int64  `json:"expires_at"`
}

// CapabilityIssuer mints and revokes capabilities. The gateway provides it; a
// function reaches it through the capability host calls.
type CapabilityIssuer interface {
	// Mint issues a capability for function's WebSocket in namespace, naming
	// resource, issued by issuerDevice, for ttl. It returns what the
	// capability grants and the token itself.
	Mint(ctx context.Context, namespace, function, resource, issuerDevice string, ttl time.Duration) (*CapabilityGrant, string, error)
	// Revoke refuses one capability of namespace from now on. It takes the
	// capability's token, which proves the namespace was issued it.
	Revoke(ctx context.Context, namespace, token string) error
}

// capabilityOpens reports whether req's capability opens fn. The gateway
// checked the token before the upgrade; this is the invoker's half of the same
// decision, so a capability reaches only a function that accepts capabilities,
// only over its WebSocket, and never an internal function.
func capabilityOpens(fn *Function, req *InvokeRequest) bool {
	return req.CallerCapability != nil &&
		req.TriggerType == TriggerTypeWebSocket &&
		fn.WSAuth == WSAuthCapability &&
		!fn.IsInternal
}
