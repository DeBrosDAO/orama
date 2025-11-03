package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	olriclib "github.com/olric-data/olric"
)

// Cache HTTP handlers for Olric distributed cache

func (g *Gateway) cacheHealthHandler(w http.ResponseWriter, r *http.Request) {
	if g.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err := g.olricClient.Health(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("cache health check failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "olric",
	})
}

func (g *Gateway) cacheGetHandler(w http.ResponseWriter, r *http.Request) {
	if g.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DMap string `json:"dmap"` // Distributed map name
		Key  string `json:"key"`  // Key to retrieve
	}

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

	client := g.olricClient.GetClient()
	dm, err := client.NewDMap(req.DMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create DMap: %v", err))
		return
	}

	gr, err := dm.Get(ctx, req.Key)
	if err != nil {
		if err == olriclib.ErrKeyNotFound {
			writeError(w, http.StatusNotFound, "key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get key: %v", err))
		return
	}

	// Try to decode the value from Olric
	// Values stored as JSON bytes need to be deserialized, while basic types
	// (strings, numbers, bools) can be retrieved directly
	var value any

	// First, try to get as bytes (for JSON-serialized complex types)
	var bytesVal []byte
	if err := gr.Scan(&bytesVal); err == nil && len(bytesVal) > 0 {
		// Try to deserialize as JSON
		var jsonVal any
		if err := json.Unmarshal(bytesVal, &jsonVal); err == nil {
			value = jsonVal
		} else {
			// If JSON unmarshal fails, treat as string
			value = string(bytesVal)
		}
	} else {
		// Try as string (for simple string values)
		if strVal, err := gr.String(); err == nil {
			value = strVal
		} else {
			// Fallback: try to scan as any type
			var anyVal any
			if err := gr.Scan(&anyVal); err == nil {
				value = anyVal
			} else {
				// Last resort: try String() again, ignoring error
				strVal, _ := gr.String()
				value = strVal
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":   req.Key,
		"value": value,
		"dmap":  req.DMap,
	})
}

func (g *Gateway) cachePutHandler(w http.ResponseWriter, r *http.Request) {
	if g.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DMap  string `json:"dmap"`  // Distributed map name
		Key   string `json:"key"`   // Key to store
		Value any    `json:"value"` // Value to store
		TTL   string `json:"ttl"`   // Optional TTL (duration string like "1h", "30m")
	}

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

	client := g.olricClient.GetClient()
	dm, err := client.NewDMap(req.DMap)
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
	var valueToStore any
	switch req.Value.(type) {
	case map[string]any:
		// Serialize maps to JSON bytes
		jsonBytes, err := json.Marshal(req.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to marshal value: %v", err))
			return
		}
		valueToStore = jsonBytes
	case []any:
		// Serialize slices to JSON bytes
		jsonBytes, err := json.Marshal(req.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to marshal value: %v", err))
			return
		}
		valueToStore = jsonBytes
	case string:
		// Basic string type can be stored directly
		valueToStore = req.Value
	case float64:
		// Basic number type can be stored directly
		valueToStore = req.Value
	case int:
		// Basic int type can be stored directly
		valueToStore = req.Value
	case int64:
		// Basic int64 type can be stored directly
		valueToStore = req.Value
	case bool:
		// Basic bool type can be stored directly
		valueToStore = req.Value
	case nil:
		// Nil can be stored directly
		valueToStore = req.Value
	default:
		// For any other type, serialize to JSON to be safe
		jsonBytes, err := json.Marshal(req.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to marshal value: %v", err))
			return
		}
		valueToStore = jsonBytes
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

func (g *Gateway) cacheDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if g.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DMap string `json:"dmap"` // Distributed map name
		Key  string `json:"key"`  // Key to delete
	}

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

	client := g.olricClient.GetClient()
	dm, err := client.NewDMap(req.DMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create DMap: %v", err))
		return
	}

	deletedCount, err := dm.Delete(ctx, req.Key)
	if err != nil {
		if err == olriclib.ErrKeyNotFound {
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

func (g *Gateway) cacheScanHandler(w http.ResponseWriter, r *http.Request) {
	if g.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		DMap  string `json:"dmap"`  // Distributed map name
		Match string `json:"match"` // Optional regex pattern to match keys
	}

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

	client := g.olricClient.GetClient()
	dm, err := client.NewDMap(req.DMap)
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
