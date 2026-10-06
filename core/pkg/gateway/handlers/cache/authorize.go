package cache

import (
	"net/http"
	"strings"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A grant may be narrowed to a key pattern — `cache:key=sessions/*` — and this
// is where that is applied.
//
// Before it, a `cache` grant was every key in every distributed map the
// namespace had, so one leaked runtime key read every session the application
// had cached. A credential with no selector is unaffected: the scope gate has
// already decided whether it may reach the cache at all, and this only ever
// takes access away.

// cacheResourceName is what a selector matches against: the map and the key,
// joined.
//
// Joining them is deliberate. A namespace has several distributed maps and the
// useful narrowing is usually "this whole map" — `cache:key=sessions/*` — which
// a key-only comparison could not express. The separator is `/` because that is
// what the storage selectors use and `*` crosses it, so a map-wide pattern
// needs no special case.
func cacheResourceName(dmap, key string) string {
	dmap, key = strings.TrimSpace(dmap), strings.TrimSpace(key)
	if key == "" {
		return dmap
	}
	return dmap + "/" + key
}

// authorizeKey refuses a request whose grant does not cover the key.
func (h *CacheHandlers) authorizeKey(w http.ResponseWriter, r *http.Request, dmap, key string, action gwauth.Action) bool {
	err := gwauth.AuthorizeResource(r.Context(), gwauth.Resource{
		Domain: gwauth.SelectorCache,
		Name:   cacheResourceName(dmap, key),
		Action: action,
	})
	if err == nil {
		return true
	}
	writeError(w, http.StatusForbidden, err.Error())
	return false
}

// authorizeKeys refuses a multi-key request unless the grant covers every key
// in it.
//
// All or nothing, rather than filtering to the permitted subset: a partial
// answer to `mget` is indistinguishable from keys that were not set, so a
// caller would read a silently narrowed result as a complete one.
func (h *CacheHandlers) authorizeKeys(w http.ResponseWriter, r *http.Request, dmap string, keys []string, action gwauth.Action) bool {
	for _, key := range keys {
		if !h.authorizeKey(w, r, dmap, key, action) {
			return false
		}
	}
	return true
}
