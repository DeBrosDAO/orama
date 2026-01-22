package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	olriclib "github.com/olric-data/olric"
)

// ScanHandler handles cache SCAN/LIST requests for listing keys in a distributed map.
// It expects a JSON body with "dmap" (distributed map name) and optionally "match" (regex pattern).
// Returns all keys in the map, or only keys matching the pattern if provided.
//
// Request body:
//
//	{
//	  "dmap": "my-cache",
//	  "match": "user:*"  // Optional: regex pattern to filter keys
//	}
//
// Response:
//
//	{
//	  "keys": ["user:123", "user:456"],
//	  "count": 2,
//	  "dmap": "my-cache"
//	}
func (h *CacheHandlers) ScanHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" {
		writeError(w, http.StatusBadRequest, "dmap is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
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

	var iterator olriclib.Iterator
	if req.Match != "" {
		iterator, err = dm.Scan(ctx, olriclib.Match(req.Match))
	} else {
		iterator, err = dm.Scan(ctx)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to scan: %v", err))
		return
	}
	defer iterator.Close()

	var keys []string
	for iterator.Next() {
		keys = append(keys, iterator.Key())
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"keys":  keys,
		"count": len(keys),
		"dmap":  req.DMap,
	})
}

// HealthHandler handles health check requests for the Olric cache service.
// Returns 200 OK if the cache is healthy, or 503 Service Unavailable if not.
//
// Response (success):
//
//	{
//	  "status": "ok",
//	  "service": "olric"
//	}
//
// Response (failure):
//
//	{
//	  "error": "cache health check failed: ..."
//	}
func (h *CacheHandlers) HealthHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err := h.olricClient.Health(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("cache health check failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "olric",
	})
}
