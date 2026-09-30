package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
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
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
	var req DeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" || strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "dmap and key are required")
		return
	}

	if !h.authorizeKey(w, r, req.DMap, req.Key, gwauth.ActionWrite) {
		return
	}

	// The availability check comes after the request has been read and
	// authorized. A caller who may not touch this key is refused whether or not
	// the cache happens to be up, and a 503 would otherwise tell them the cache
	// exists and is down — an answer they are not entitled to.
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
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
		h.writeCacheFailure(w, http.StatusInternalServerError, "failed to create DMap", err)
		return
	}

	// Whether the key exists is asked of its owner first. Olric's Delete count
	// cannot answer it (olric v0.7.4 internal/dmap/delete.go deleteKeys): a key
	// whose partition another member owns is forwarded, deleted, and reported
	// as 0; one this member owns is reported as deleted whether or not it was
	// there. Reading the count turned every delete of a key held on another
	// member into "key not found".
	if _, err := dm.Get(ctx, req.Key); err != nil {
		if isKeyNotFound(err) {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		h.writeCacheFailure(w, http.StatusInternalServerError, "failed to look the key up", err)
		return
	}
	if _, err := dm.Delete(ctx, req.Key); err != nil && !isKeyNotFound(err) {
		h.writeCacheFailure(w, http.StatusInternalServerError, "failed to delete key", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    req.Key,
		"dmap":   req.DMap,
	})
}

// isKeyNotFound reports Olric's missing-key answer, wrapped or not.
func isKeyNotFound(err error) bool {
	return errors.Is(err, olriclib.ErrKeyNotFound) || strings.Contains(err.Error(), "key not found")
}
