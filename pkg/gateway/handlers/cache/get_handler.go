package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	olriclib "github.com/olric-data/olric"
	"go.uber.org/zap"
)

// GetHandler handles cache GET requests for retrieving a single key from a distributed map.
// It expects a JSON body with "dmap" (distributed map name) and "key" fields.
// Returns the value associated with the key, or 404 if the key is not found.
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
//	  "key": "user:123",
//	  "value": {...},
//	  "dmap": "my-cache"
//	}
func (h *CacheHandlers) GetHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req GetRequest
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

	olricCluster := h.olricClient.GetClient()
	dm, err := olricCluster.NewDMap(req.DMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create DMap: %v", err))
		return
	}

	gr, err := dm.Get(ctx, req.Key)
	if err != nil {
		// Check for key not found error - handle both wrapped and direct errors
		if errors.Is(err, olriclib.ErrKeyNotFound) || err.Error() == "key not found" || strings.Contains(err.Error(), "key not found") {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		h.logger.ComponentError(logging.ComponentGeneral, "failed to get key from cache",
			zap.String("dmap", req.DMap),
			zap.String("key", req.Key),
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get key: %v", err))
		return
	}

	value, err := decodeValueFromOlric(gr)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to decode value from cache",
			zap.String("dmap", req.DMap),
			zap.String("key", req.Key),
			zap.Error(err))
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to decode value: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":   req.Key,
		"value": value,
		"dmap":  req.DMap,
	})
}

// MultiGetHandler handles cache multi-GET requests for retrieving multiple keys from a distributed map.
// It expects a JSON body with "dmap" (distributed map name) and "keys" (array of keys) fields.
// Returns only the keys that were found; missing keys are silently skipped.
//
// Request body:
//
//	{
//	  "dmap": "my-cache",
//	  "keys": ["user:123", "user:456"]
//	}
//
// Response:
//
//	{
//	  "results": [
//	    {"key": "user:123", "value": {...}},
//	    {"key": "user:456", "value": {...}}
//	  ],
//	  "dmap": "my-cache"
//	}
func (h *CacheHandlers) MultiGetHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req MultiGetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" {
		writeError(w, http.StatusBadRequest, "dmap is required")
		return
	}

	if len(req.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "keys array is required and cannot be empty")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	olricCluster := h.olricClient.GetClient()
	dm, err := olricCluster.NewDMap(req.DMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create DMap: %v", err))
		return
	}

	// Get all keys and collect results
	var results []map[string]any
	for _, key := range req.Keys {
		if strings.TrimSpace(key) == "" {
			continue // Skip empty keys
		}

		gr, err := dm.Get(ctx, key)
		if err != nil {
			// Skip keys that are not found - don't include them in results
			// This matches the SDK's expectation that only found keys are returned
			if err == olriclib.ErrKeyNotFound {
				continue
			}
			// For other errors, log but continue with other keys
			// We don't want one bad key to fail the entire request
			continue
		}

		value, err := decodeValueFromOlric(gr)
		if err != nil {
			// If we can't decode, skip this key
			continue
		}

		results = append(results, map[string]any{
			"key":   key,
			"value": value,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"dmap":    req.DMap,
	})
}

// writeJSON writes JSON response with the specified status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a standardized JSON error response.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
