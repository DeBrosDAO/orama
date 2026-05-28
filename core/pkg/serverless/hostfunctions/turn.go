package hostfunctions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"github.com/DeBrosOfficial/network/pkg/turn"
)

// turnCredentialTTL mirrors the HTTP handler at
// pkg/gateway/handlers/webrtc/credentials.go — the credentials are
// time-bound HMAC tokens and 10min is the operational sweet spot
// (long enough for a call to set up, short enough to limit replay
// exposure if a token leaks).
const turnCredentialTTL = 10 * time.Minute

// turnCredentialsEnvelope is the JSON shape returned by TurnCredentials.
// Mirrors what the HTTP credentials endpoint returns at the wire so
// WASM callers and JS clients see the same field names — keeps SDKs
// trivial. `configured=false` means TURN isn't set up on this gateway
// (TURNSecret empty); callers should fall back to STUN-only.
type turnCredentialsEnvelope struct {
	Configured bool     `json:"configured"`
	Username   string   `json:"username,omitempty"`
	Password   string   `json:"password,omitempty"`
	TTL        int      `json:"ttl,omitempty"` // seconds
	URIs       []string `json:"uris,omitempty"`
	Namespace  string   `json:"namespace,omitempty"`
}

// TurnCredentials implements feat-9 — minting TURN credentials inside a
// WASM function without a round-trip through HTTP. Mirrors the
// `POST /v1/webrtc/turn/credentials` endpoint exactly: derives the
// namespace from the invocation context (caller cannot spoof), generates
// per-namespace HMAC credentials via pkg/turn, and assembles the same
// URI list (including stealth TURN-over-443 when StealthCDNDomain is
// set).
//
// Returns a JSON envelope identical in shape to the HTTP response, so
// the WASM-side SDK helper can return it as-is to in-process callers
// who want to inject the creds into RTCPeerConnection config.
//
// Setup-failure semantics match the rest of the host-fn family:
//   - No namespace in invocation context → Go error (HostFunctionError).
//     This should never happen in normal serverless flow but is defensive.
//   - TURN not configured on this gateway (TURNSecret empty) → returns
//     {configured:false} as a structured envelope, NOT an error. Same
//     shape as PushSend's silent no-op when push isn't configured —
//     keeps functions portable across deployments.
func (h *HostFunctions) TurnCredentials(ctx context.Context) ([]byte, error) {
	cur := h.currentInvocationContext(ctx)
	if cur == nil || cur.Namespace == "" {
		return nil, &serverless.HostFunctionError{
			Function: "turn_credentials",
			Cause:    fmt.Errorf("no namespace in invocation context"),
		}
	}

	if h.turnSecret == "" {
		// TURN not configured on this gateway — return structured
		// "not configured" envelope so the caller can fall back to
		// STUN-only without treating it as a function-level error.
		// Matches the HTTP handler's 503 semantically, but at the host-
		// fn boundary we encode it as a result shape, not a Go error.
		return json.Marshal(turnCredentialsEnvelope{
			Configured: false,
			Namespace:  cur.Namespace,
		})
	}

	username, password := turn.GenerateCredentials(h.turnSecret, cur.Namespace, turnCredentialTTL)

	uris := buildTURNURIs(h.turnDomain, h.stealthCDNDomain)

	return json.Marshal(turnCredentialsEnvelope{
		Configured: true,
		Username:   username,
		Password:   password,
		TTL:        int(turnCredentialTTL.Seconds()),
		URIs:       uris,
		Namespace:  cur.Namespace,
	})
}

// buildTURNURIs is the URI assembly shared between the host-fn path and
// the HTTP credentials handler. Returns an empty slice when neither
// turnDomain nor stealthCDNDomain is set — caller-side this means
// "TURN reachable but no public URI to advertise", which is a config
// problem the operator should fix.
//
// Stealth: when stealthCDNDomain is non-empty we append
// `turns:<domain>:443` — that endpoint is served by the in-house SNI
// router on the standard HTTPS port and looks like ordinary TLS to a
// passive observer / DPI. Usable in restricted regions.
func buildTURNURIs(turnDomain, stealthCDNDomain string) []string {
	var uris []string
	if turnDomain != "" {
		uris = append(uris,
			fmt.Sprintf("turn:%s:3478?transport=udp", turnDomain),
			fmt.Sprintf("turn:%s:3478?transport=tcp", turnDomain),
			fmt.Sprintf("turns:%s:5349", turnDomain),
		)
	}
	if stealthCDNDomain != "" {
		uris = append(uris, fmt.Sprintf("turns:%s:443", stealthCDNDomain))
	}
	return uris
}
