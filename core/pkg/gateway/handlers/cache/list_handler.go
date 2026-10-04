package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
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
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB
	var req ScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	if strings.TrimSpace(req.DMap) == "" {
		writeError(w, http.StatusBadRequest, "dmap is required")
		return
	}
	if len(req.Match) > MaxMatchBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("match pattern too long: at most %d bytes", MaxMatchBytes))
		return
	}

	// The availability check comes after the request has been read, so a
	// malformed one is reported as malformed whether or not the cache is up.
	if h.olricClient == nil {
		writeError(w, http.StatusServiceUnavailable, "Olric cache client not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// A bad pattern is the caller's to fix, and is found before the cache is asked.
	var match *regexp.Regexp
	if req.Match != "" {
		var err error
		if match, err = regexp.Compile(req.Match); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid match pattern: %v", err))
			return
		}
	}

	// Namespace isolation: the namespace's one cache DMap (namespace_dmap.go).
	dm, ok := h.namespaceCache(ctx, w)
	if !ok {
		return
	}

	// The DMap holds every dmap of the namespace, so Olric lists the keys that
	// start with this dmap's prefix and the pattern is applied here, to the
	// tenant's key, which is what the caller's pattern was written against.
	iterator, err := dm.Scan(ctx, olriclib.Match("^"+regexp.QuoteMeta(dmapKeyPrefix(req.DMap))))
	if err != nil {
		h.writeCacheFailure(w, http.StatusInternalServerError, "failed to scan", err)
		return
	}
	defer iterator.Close()

	// A scan asks "what is here", so the answer is what this credential may
	// see. That is filtering rather than refusing, and the two are different on
	// purpose: mget names its keys and a missing one reads as unset, where a
	// scan's whole answer is the set it returns.
	var keys []string
	for iterator.Next() {
		key, ok := unfoldKey(req.DMap, iterator.Key())
		if !ok || (match != nil && !match.MatchString(key)) {
			continue
		}
		if gwauth.AuthorizeResource(r.Context(), gwauth.Resource{
			Domain: gwauth.SelectorCache,
			Name:   cacheResourceName(req.DMap, key),
			Action: gwauth.ActionRead,
		}) != nil {
			continue
		}
		keys = append(keys, key)
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
		h.writeCacheFailure(w, http.StatusServiceUnavailable, "cache health check failed", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "olric",
	})
}
