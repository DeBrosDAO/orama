package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// guardianChallengeResponse is the guardian's response to POST /v1/vault/auth/challenge.
type guardianChallengeResponse struct {
	Nonce     string `json:"nonce"`
	CreatedNs int64  `json:"created_ns"`
	Tag       string `json:"tag"`
}

// guardianSessionResponse is the guardian's response to POST /v1/vault/auth/session.
type guardianSessionResponse struct {
	Identity string `json:"identity"`
	ExpiryNs int64  `json:"expiry_ns"`
	Tag      string `json:"tag"`
}

// authenticateGuardian runs the HMAC challenge/session handshake with a single
// guardian and returns an X-Session-Token value bound to identity.
//
// The guardian requires a valid session token on every push/pull whenever it is
// configured (always, in production) — without this the guardian answers 401 and
// no share is stored, which is why the gateway proxy previously stored nothing.
//
// Each guardian mints tokens under its own per-process server_secret, so tokens
// are obtained per-guardian and are not cached or shared across guardians (and
// would be invalidated by a guardian restart anyway). We therefore authenticate
// inline per request; the extra two round-trips per guardian are acceptable for a
// debounced backup sync and avoid any stale-token failure mode.
func (h *Handlers) authenticateGuardian(ctx context.Context, ip string, port int, identity string) (string, error) {
	// 1. Request a challenge.
	chalBody, _ := json.Marshal(map[string]string{"identity": identity})
	chalURL := fmt.Sprintf("http://%s:%d/v1/vault/auth/challenge", ip, port)
	chal, err := h.doGuardianJSON(ctx, chalURL, chalBody)
	if err != nil {
		return "", fmt.Errorf("challenge: %w", err)
	}
	var challenge guardianChallengeResponse
	if err := json.Unmarshal(chal, &challenge); err != nil {
		return "", fmt.Errorf("challenge decode: %w", err)
	}

	// 2. Exchange the verified challenge for a session token.
	sessBody, _ := json.Marshal(map[string]interface{}{
		"identity":   identity,
		"nonce":      challenge.Nonce,
		"created_ns": challenge.CreatedNs,
		"tag":        challenge.Tag,
	})
	sessURL := fmt.Sprintf("http://%s:%d/v1/vault/auth/session", ip, port)
	sessRaw, err := h.doGuardianJSON(ctx, sessURL, sessBody)
	if err != nil {
		return "", fmt.Errorf("session: %w", err)
	}
	var sess guardianSessionResponse
	if err := json.Unmarshal(sessRaw, &sess); err != nil {
		return "", fmt.Errorf("session decode: %w", err)
	}

	// Token format expected by the guardian's validateSessionToken:
	//   <identity_hex>:<expiry_ns>:<tag_hex>
	// Use the ORIGINAL identity we sent (64 hex) for the token segment — the
	// guardian re-hashes exactly that segment to check the tag. (The session
	// response's own `identity` field is unreliable; the guardian historically
	// double-hex-encoded it to 128 chars, which validateSessionToken rejects.)
	return fmt.Sprintf("%s:%d:%s", identity, sess.ExpiryNs, sess.Tag), nil
}

// doGuardianJSON POSTs a JSON body to a guardian endpoint and returns the raw
// 2xx response body, or an error for transport/status failures.
func (h *Handlers) doGuardianJSON(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxPullBodySize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return respBody, nil
}
