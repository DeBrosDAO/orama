package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// Rotating the key this gateway signs with.
//
// There was nothing to rotate before: the key was HKDF-derived from the cluster
// secret with a fixed label, so the only way to change it was to change the
// cluster secret, which invalidates every token in the cluster at once. Each
// gateway has its own key now, and rotating one is a local operation that
// leaves the outgoing key verifiable until the tokens it signed expire.

// handleRotateSigningKey serves POST /v1/operator/rotate-signing-key.
func (g *Gateway) handleRotateSigningKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if g.authService == nil {
		writeError(w, http.StatusServiceUnavailable, "auth service not initialized")
		return
	}
	// The admin grant is not enough. Rotating the index gateway's key is a
	// cluster operation, and the wallet has to be on the operator list — the
	// same check every other /v1/operator route makes.
	if g.operatorHandler == nil {
		writeError(w, http.StatusServiceUnavailable, "operator endpoints are not enabled on this gateway")
		return
	}
	if _, ok := g.operatorHandler.Authorize(w, r); !ok {
		return
	}

	if g.cfg == nil || g.cfg.DataDir == "" {
		writeError(w, http.StatusServiceUnavailable,
			"this gateway has no data directory, so a replacement key has nowhere to be written")
		return
	}

	previous := g.authService.SigningKID()
	next, err := g.authService.Rotate(r.Context(), g.cfg.DataDir)
	if err != nil {
		g.logger.ComponentWarn("gateway", "signing key rotation failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to rotate the signing key: "+err.Error())
		return
	}

	g.authService.Audit().RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Actor:    auth.ActorFromRequest(r),
		Action:   auth.AuditOperatorAction,
		Resource: "signing-key.rotate",
		Result:   auth.AuditSuccess,
		Metadata: map[string]string{"previous_kid": previous, "kid": next.KID},
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"kid":          next.KID,
		"previous_kid": previous,
		"namespace":    next.Namespace,
		// The outgoing key keeps verifying for this long. Nothing has to be
		// restarted and nobody is signed out.
		"previous_accepted_for": auth.AccessTokenLifetime.String(),
	})
}
