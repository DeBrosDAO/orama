package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	olriclib "github.com/olric-data/olric"
)

// DeleteHandler handles cache DELETE requests for removing a key from a distributed map.
// It expects a JSON body with "dmap" (distributed map name) and "key" fields.
// Returns 404 if the key is not found, or 200 if successfully deleted.
//
// Request body:
//
//	{
//	  "dmap": "my-cache",
//	  "key": "user:123"
//	}
//
// Response:
//
//	{
//	  "status": "ok",
//	  "key": "user:123",
//	  "dmap": "my-cache"
//	}
func (h *CacheHandlers) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req DeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" || strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "dmap and key are required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Namespace isolation: prefix dmap with namespace
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		writeError(w, http.StatusUnauthorized, "namespace not found in context")
		return
	}
	namespacedDMap := fmt.Sprintf("%s:%s", namespace, req.DMap)

	olricCluster := h.olricClient.GetClient()
	dm, err := olricCluster.NewDMap(namespacedDMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create DMap: %v", err))
		return
	}

	deletedCount, err := dm.Delete(ctx, req.Key)
	if err != nil {
		// Check for key not found error - handle both wrapped and direct errors
		if errors.Is(err, olriclib.ErrKeyNotFound) || err.Error() == "key not found" || strings.Contains(err.Error(), "key not found") {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete key: %v", err))
		return
	}
	if deletedCount == 0 {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    req.Key,
		"dmap":   req.DMap,
	})
}
