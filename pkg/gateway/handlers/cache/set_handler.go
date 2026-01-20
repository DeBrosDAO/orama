package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SetHandler handles cache PUT/SET requests for storing a key-value pair in a distributed map.
// It expects a JSON body with "dmap", "key", and "value" fields, and optionally "ttl".
// The value can be any JSON-serializable type (string, number, object, array, etc.).
// Complex types (maps, arrays) are automatically serialized to JSON bytes for storage.
//
// Request body:
//
//	{
//	  "dmap": "my-cache",
//	  "key": "user:123",
//	  "value": {"name": "John", "age": 30},
//	  "ttl": "1h"  // Optional: "1h", "30m", etc.
//	}
//
// Response:
//
//	{
//	  "status": "ok",
//	  "key": "user:123",
//	  "dmap": "my-cache"
//	}
func (h *CacheHandlers) SetHandler(w http.ResponseWriter, r *http.Request) {
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req PutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" || strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "dmap and key are required")
		return
	}

	if req.Value == nil {
		writeError(w, http.StatusBadRequest, "value is required")
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

	// TODO: TTL support - need to check Olric v0.7 API for TTL/expiry options
	// For now, ignore TTL if provided
	if req.TTL != "" {
		_, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid ttl format: %v", err))
			return
		}
		// TTL parsing succeeded but not yet implemented in API
		// Will be added once we confirm the correct Olric API method
	}

	// Serialize complex types (maps, slices) to JSON bytes for Olric storage
	// Olric can handle basic types (string, number, bool) directly, but complex
	// types need to be serialized to bytes
	valueToStore, err := prepareValueForStorage(req.Value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to prepare value: %v", err))
		return
	}

	err = dm.Put(ctx, req.Key, valueToStore)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to put key: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    req.Key,
		"dmap":   req.DMap,
	})
}

// prepareValueForStorage prepares a value for storage in Olric.
// Complex types (maps, slices) are serialized to JSON bytes.
// Basic types (string, number, bool) are stored directly.
func prepareValueForStorage(value any) (any, error) {
	switch value.(type) {
	case map[string]any:
		// Serialize maps to JSON bytes
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal map value: %w", err)
		}
		return jsonBytes, nil
	case []any:
		// Serialize slices to JSON bytes
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal array value: %w", err)
		}
		return jsonBytes, nil
	case string, float64, int, int64, bool, nil:
		// Basic types can be stored directly
		return value, nil
	default:
		// For any other type, serialize to JSON to be safe
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal value: %w", err)
		}
		return jsonBytes, nil
	}
}
