package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/olric"
	olriclib "github.com/olric-data/olric"
)

// getNamespaceFromContext extracts the namespace from the request context
func getNamespaceFromContext(ctx context.Context) string {
	if ns, ok := ctx.Value(ctxkeys.NamespaceOverride).(string); ok {
		return ns
	}
	return ""
}

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
//	  "ttl": "1h"  // Optional: "1h", "30m", etc. Omitted or "0s" = no expiry.
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
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
	var req PutRequest
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

	if req.Value == nil {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}

	putOpts, err := putOptionsForTTL(req.TTL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	// Serialize complex types (maps, slices) to JSON bytes for Olric storage
	// Olric can handle basic types (string, number, bool) directly, but complex
	// types need to be serialized to bytes
	valueToStore, err := prepareValueForStorage(req.Value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to prepare value: %v", err))
		return
	}

	err = dm.Put(ctx, req.Key, valueToStore, putOpts...)
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

// putOptionsForTTL turns the request's optional ttl into Olric put options.
// An empty or zero ttl ("", "0", "0s") stores the entry with no expiry,
// matching cache_set's ttl=0. A ttl that does not parse, is negative, or is
// longer than olric.MaxEntryTTL is refused rather than stored as something it
// is not.
func putOptionsForTTL(ttl string) ([]olriclib.PutOption, error) {
	if ttl == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(ttl)
	if err != nil {
		return nil, fmt.Errorf("invalid ttl format: %w", err)
	}
	switch {
	case d < 0:
		return nil, fmt.Errorf("ttl must not be negative, got %q; omit ttl or send \"0s\" for no expiry", ttl)
	case d > olric.MaxEntryTTL:
		return nil, fmt.Errorf("ttl %q exceeds the maximum of %s", ttl, olric.MaxEntryTTL)
	case d == 0:
		return nil, nil
	}
	return []olriclib.PutOption{olriclib.EX(d)}, nil
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
