package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// VerifyHandler verifies a wallet signature and issues JWT tokens and an API key.
// This completes the authentication flow by validating the signed nonce and returning
// access credentials. For non-default namespaces, may trigger cluster provisioning
// and return 202 Accepted with credentials + poll URL.
//
// POST /v1/auth/verify
// Request body: VerifyRequest
// Response 200: { "access_token", "token_type", "expires_in", "refresh_token", "subject", "namespace", "nonce", "signature_verified" }
// plus "api_key", except in the lobby namespace, on a device-bound sign-in, and for
// a member whose role holds no grant (a reader), none of which gets one.
// Response 202: { "status": "provisioning", "cluster_id", "poll_url", "access_token", "refresh_token", "api_key", ... }
func (h *Handlers) VerifyHandler(w http.ResponseWriter, r *http.Request) {
	if h.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64*1024) // 64KB
	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Message) == "" || strings.TrimSpace(req.Signature) == "" {
		writeError(w, http.StatusBadRequest, "message and signature are required: sign the message "+
			"returned by /v1/auth/challenge and send it back verbatim")
		return
	}

	ctx := r.Context()
	in, ok := h.signIn(w, r, req.Message, req.Signature)
	if !ok {
		return
	}
	wallet, namespace := in.Wallet, in.Namespace

	// Refuse before anything is issued or provisioned: a namespace that belongs
	// to another wallet is not this caller's to sign in to.
	if err := h.authService.RequireSignInAllowed(ctx, wallet, namespace); err != nil {
		writeCredentialError(w, namespace, err)
		return
	}

	// Signing in does not provision anything. It used to: a challenge created
	// the namespace and verifying the signature spun up its cluster, so an
	// anonymous caller could create infrastructure by naming a name. Creating a
	// namespace is POST /v1/namespaces, and that is what provisions it.
	//
	// A namespace whose cluster is still coming up is reported by
	// /v1/namespace/status, which the create path hands back a poll URL for.

	binding, ok := h.bindSignIn(w, r, in, req)
	if !ok {
		return
	}
	if binding.pending != nil {
		named, _ := authsvc.DeviceOf(in.Message)
		h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
			Namespace: namespace,
			Actor:     wallet,
			Action:    authsvc.AuditDeviceLoginStarted,
			Result:    authsvc.AuditSuccess,
			Metadata:  map[string]string{"device": named, "reason": "device awaits approval"},
		})
		writePendingDevice(w, in, named, binding.pending)
		return
	}

	token, refresh, expUnix, err := h.authService.IssueDeviceTokens(ctx, wallet, namespace, binding.deviceID)
	if err != nil {
		// The early gate let the wallet in; the namespace closed in between.
		if isSignInRefusal(err) {
			writeCredentialError(w, namespace, err)
			return
		}
		h.writeUnavailable(w, "issue the session", err)
		return
	}

	apiKey, err := signInKey(ctx, h.authService.GetOrCreateAPIKey, wallet, namespace, binding.deviceID)
	if err != nil {
		writeCredentialError(w, namespace, err)
		return
	}

	h.authService.Audit().RecordFromRequest(ctx, r, authsvc.AuditEvent{
		Namespace: namespace,
		Actor:     wallet,
		Action:    authsvc.AuditVerifySucceeded,
		Result:    authsvc.AuditSuccess,
	})

	body := map[string]any{
		"access_token":       token,
		"token_type":         "Bearer",
		"expires_in":         int(expUnix - time.Now().Unix()),
		"refresh_token":      refresh,
		"subject":            wallet,
		"namespace":          namespace,
		"nonce":              in.Message.Nonce,
		"signature_verified": true,
	}
	if apiKey != "" {
		body["api_key"] = apiKey
	}
	if binding.deviceID != "" {
		body["device_id"] = binding.deviceID
	}
	writeJSON(w, http.StatusOK, body)
}

// signInKey is the API key a sign-in hands back beside its session, or "" when
// it hands back none.
//
// The lobby has no keys. A wallet signing in there gets a session and nothing
// else; the one thing that session reaches is POST /v1/namespaces, which
// creates a namespace and makes the caller its owner.
//
// Neither does a device-bound sign-in get one. The device is the credential;
// a key for the whole account handed out beside it would outlive revoking the
// device, which is the point of binding one.
//
// Nor does a member whose role holds no grant (a reader), or a wallet that is
// no member at all (an end user of a namespace with open sign-in): there is
// nothing to put in a key, and the session alone reaches what the role, or
// having none, may reach.
func signInKey(ctx context.Context, mint func(context.Context, string, string) (string, error),
	wallet, namespace, deviceID string) (string, error) {
	if authsvc.IsLobbyNamespace(namespace) || deviceID != "" {
		return "", nil
	}
	key, err := mint(ctx, wallet, namespace)
	if errors.Is(err, authsvc.ErrNoKeyForRole) {
		return "", nil
	}
	return key, err
}
