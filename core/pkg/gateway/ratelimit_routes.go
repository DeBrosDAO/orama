package gateway

// ratelimit_routes.go — method-dispatcher for the per-namespace rate-limit
// configuration endpoint. Feature #69. Mirrors the push-config route shape.

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// rateLimitConfigDispatcher routes GET / PUT / DELETE on
// /v1/namespace/rate-limit to the respective handler. When the rate-limit
// subsystem isn't wired (older deployments without an ORM client) it
// returns a canonical 503 envelope explaining the situation — far better
// UX than a bare 404.
func (g *Gateway) rateLimitConfigDispatcher(w http.ResponseWriter, r *http.Request) {
	if g.rateLimitHandlers == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable,
			"rate-limit configuration not available on this gateway")
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.rateLimitHandlers.GetConfigHandler(w, r)
	case http.MethodPut, http.MethodPost:
		g.rateLimitHandlers.PutConfigHandler(w, r)
	case http.MethodDelete:
		g.rateLimitHandlers.DeleteConfigHandler(w, r)
	default:
		httputil.WriteRPCError(w, http.StatusMethodNotAllowed,
			httputil.ErrCodeValidationFailed,
			"method not allowed: use GET to read, PUT to update, or DELETE to clear")
	}
}
