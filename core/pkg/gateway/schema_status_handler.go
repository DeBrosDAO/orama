package gateway

// schema_status_handler.go exposes the gateway's schema-version contract
// over HTTP so namespace tenants can self-check schema drift without
// SSH access (bug #214 audit follow-up).
//
// Endpoint:
//
//	GET /v1/schema-status
//
// Response (canonical, success):
//
//	{
//	  "ok":               true,
//	  "required_version": 25,
//	  "applied_version":  25,
//	  "in_sync":          true,
//	  "pending":          []
//	}
//
// Response (drift):
//
//	{
//	  "ok":               true,
//	  "required_version": 25,
//	  "applied_version":  22,
//	  "in_sync":          false,
//	  "pending": [
//	    {"version": 23, "name": "push_devices"},
//	    {"version": 24, "name": "namespace_publish_seq"},
//	    {"version": 25, "name": "persistent_ws"}
//	  ]
//	}
//
// Authorization: this is mounted under `/v1/` so the existing
// namespace-ownership middleware applies — only the namespace's own
// authenticated callers can see it. Schema state isn't sensitive on its
// own (it's effectively a public version pin) but we gate by namespace
// to be consistent with every other admin-flavored endpoint.
//
// Why HTTP and not just the CLI: the CLI requires SSH to a node. An
// app developer or tenant ops person should be able to verify schema
// state from their workstation without infrastructure access.

import (
	"context"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// schemaStatusResponse is the canonical wire shape. Exported tag-only
// fields so other Go callers (tests, dashboards) can consume the same shape.
type schemaStatusResponse struct {
	OK              bool                `json:"ok"`
	RequiredVersion int                 `json:"required_version"`
	AppliedVersion  int                 `json:"applied_version"`
	InSync          bool                `json:"in_sync"`
	Pending         []schemaPendingItem `json:"pending"`
}

type schemaPendingItem struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
}

// handleSchemaStatus serves GET /v1/schema-status.
//
// Errors return the canonical RPC error envelope (bug #212 fix). On a
// happy path the response is the schemaStatusResponse shape above.
func (g *Gateway) handleSchemaStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httputil.WriteRPCError(w, http.StatusMethodNotAllowed,
			httputil.ErrCodeValidationFailed, "method not allowed")
		return
	}
	if g.sqlDB == nil {
		httputil.WriteRPCError(w, http.StatusServiceUnavailable,
			httputil.ErrCodeServiceUnavailable, "schema status unavailable: db not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	required := migrations.RequiredVersion()
	applied, err := migrations.AppliedVersion(ctx, g.sqlDB)
	if err != nil {
		httputil.WriteRPCError(w, http.StatusInternalServerError,
			httputil.ErrCodeInternal, "failed to read applied schema version: "+err.Error())
		return
	}

	pending, err := migrations.PendingMigrations(ctx, g.sqlDB)
	if err != nil {
		// Non-fatal: applied/required are still useful even if pending fetch fails.
		pending = nil
	}

	items := make([]schemaPendingItem, 0, len(pending))
	for _, p := range pending {
		items = append(items, schemaPendingItem{Version: p.Version, Name: p.Name})
	}

	httputil.WriteJSON(w, http.StatusOK, schemaStatusResponse{
		OK:              true,
		RequiredVersion: required,
		AppliedVersion:  applied,
		InSync:          applied >= required,
		Pending:         items,
	})
}
