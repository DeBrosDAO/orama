package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/storage"
)

// appsHandler implements minimal CRUD for apps within a namespace.
// Routes handled:
//   - GET  /v1/apps                 -> list
//   - POST /v1/apps                 -> create
//   - GET  /v1/apps/{app_id}        -> fetch
//   - PUT  /v1/apps/{app_id}        -> update (name/public_key)
//   - DELETE /v1/apps/{app_id}      -> delete
func (g *Gateway) appsHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ctx := r.Context()
	ns := g.cfg.ClientNamespace
	if v := ctx.Value(storage.CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}
	if strings.TrimSpace(ns) == "" {
		ns = "default"
	}
	db := g.client.Database()
	nsID, err := g.resolveNamespaceID(ctx, ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	path := r.URL.Path
	// Determine if operating on collection or single resource
	if path == "/v1/apps" || path == "/v1/apps/" {
		switch r.Method {
		case http.MethodGet:
			// List apps
			res, err := db.Query(ctx, "SELECT app_id, name, public_key, created_at FROM apps WHERE namespace_id = ? ORDER BY created_at DESC", nsID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			items := make([]map[string]any, 0, res.Count)
			for _, row := range res.Rows {
				item := map[string]any{
					"app_id":     row[0],
					"name":       row[1],
					"public_key": row[2],
					"namespace":  ns,
					"created_at": row[3],
				}
				items = append(items, item)
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
			return
		case http.MethodPost:
			// Create app with provided name/public_key
			var req struct {
				Name      string `json:"name"`
				PublicKey string `json:"public_key"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			// Generate app_id
			buf := make([]byte, 12)
			if _, err := rand.Read(buf); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to generate app id")
				return
			}
			appID := "app_" + base64.RawURLEncoding.EncodeToString(buf)
			if _, err := db.Query(ctx, "INSERT INTO apps(namespace_id, app_id, name, public_key) VALUES (?, ?, ?, ?)", nsID, appID, req.Name, req.PublicKey); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"app_id":     appID,
				"name":       req.Name,
				"public_key": req.PublicKey,
				"namespace":  ns,
			})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	// Single resource: /v1/apps/{app_id}
	if strings.HasPrefix(path, "/v1/apps/") {
		appID := strings.TrimPrefix(path, "/v1/apps/")
		appID = strings.TrimSpace(appID)
		if appID == "" {
			writeError(w, http.StatusBadRequest, "missing app_id")
			return
		}
		switch r.Method {
		case http.MethodGet:
			res, err := db.Query(ctx, "SELECT app_id, name, public_key, created_at FROM apps WHERE namespace_id = ? AND app_id = ? LIMIT 1", nsID, appID)
			if err != nil || res == nil || res.Count == 0 {
				writeError(w, http.StatusNotFound, "app not found")
				return
			}
			row := res.Rows[0]
			writeJSON(w, http.StatusOK, map[string]any{
				"app_id":     row[0],
				"name":       row[1],
				"public_key": row[2],
				"namespace":  ns,
				"created_at": row[3],
			})
			return
		case http.MethodPut:
			var req struct {
				Name      *string `json:"name"`
				PublicKey *string `json:"public_key"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			// Build update dynamically
			sets := make([]string, 0, 2)
			args := make([]any, 0, 4)
			if req.Name != nil {
				sets = append(sets, "name = ?")
				args = append(args, *req.Name)
			}
			if req.PublicKey != nil {
				sets = append(sets, "public_key = ?")
				args = append(args, *req.PublicKey)
			}
			if len(sets) == 0 {
				writeError(w, http.StatusBadRequest, "no fields to update")
				return
			}
			q := "UPDATE apps SET " + strings.Join(sets, ", ") + " WHERE namespace_id = ? AND app_id = ?"
			args = append(args, nsID, appID)
			if _, err := db.Query(ctx, q, args...); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		case http.MethodDelete:
			if _, err := db.Query(ctx, "DELETE FROM apps WHERE namespace_id = ? AND app_id = ?", nsID, appID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	writeError(w, http.StatusNotFound, "not found")
}
