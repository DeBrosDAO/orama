package cache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// The selector is applied before the cache is touched, which is what lets these
// run without an Olric cluster: a refused request never reaches one, and a
// permitted one gets as far as "cache client not initialized".
//
// That ordering is the point of the test as much as the outcome. Checking after
// the read would mean the value had already been fetched.

// requestWithGrant builds a cache request carrying a narrowed grant.
func requestWithGrant(t *testing.T, path string, body any, selector string) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat")
	if selector != "" {
		ctx = context.WithValue(ctx, ctxkeys.Grant,
			&gwauth.Grant{Role: gwauth.RoleRuntime, Resource: selector})
	}
	return r.WithContext(ctx)
}

func TestCacheHandlers_applyTheKeySelector(t *testing.T) {
	h := &CacheHandlers{}

	for _, tc := range []struct {
		name    string
		call    func(http.ResponseWriter, *http.Request)
		path    string
		body    any
		refused bool
	}{
		{"get inside the selector", h.GetHandler, "/v1/cache/get",
			GetRequest{DMap: "sessions", Key: "user:1"}, false},
		{"get outside it", h.GetHandler, "/v1/cache/get",
			GetRequest{DMap: "tokens", Key: "user:1"}, true},
		{"put inside", h.SetHandler, "/v1/cache/put",
			PutRequest{DMap: "sessions", Key: "user:1", Value: "x"}, false},
		{"put outside", h.SetHandler, "/v1/cache/put",
			PutRequest{DMap: "tokens", Key: "user:1", Value: "x"}, true},
		{"delete inside", h.DeleteHandler, "/v1/cache/delete",
			DeleteRequest{DMap: "sessions", Key: "user:1"}, false},
		{"delete outside", h.DeleteHandler, "/v1/cache/delete",
			DeleteRequest{DMap: "tokens", Key: "user:1"}, true},
		{"mget all inside", h.MultiGetHandler, "/v1/cache/mget",
			MultiGetRequest{DMap: "sessions", Keys: []string{"user:1", "user:2"}}, false},
		{"mget with one key outside", h.MultiGetHandler, "/v1/cache/mget",
			MultiGetRequest{DMap: "sessions", Keys: []string{"user:1"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec, requestWithGrant(t, tc.path, tc.body, "cache:key=sessions/*"))

			if tc.refused && rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if !tc.refused && rec.Code == http.StatusForbidden {
				t.Fatalf("a key inside the selector was refused: %s", rec.Body.String())
			}
		})
	}
}

// A partial answer to mget is indistinguishable from keys that were never set,
// so one key outside the selector refuses the whole request rather than
// quietly returning less.
func TestMultiGetHandler_isAllOrNothing(t *testing.T) {
	h := &CacheHandlers{}

	t.Run("every key inside", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.MultiGetHandler(rec, requestWithGrant(t, "/v1/cache/mget",
			MultiGetRequest{DMap: "sessions", Keys: []string{"user:1", "user:2"}},
			"cache:key=sessions/*"))
		if rec.Code == http.StatusForbidden {
			t.Fatalf("keys inside the selector were refused: %s", rec.Body.String())
		}
	})

	t.Run("one key in another map", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.MultiGetHandler(rec, requestWithGrant(t, "/v1/cache/mget",
			MultiGetRequest{DMap: "tokens", Keys: []string{"user:1"}},
			"cache:key=sessions/*"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d — a key outside the selector was not refused: %s",
				rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

// A cache key is a key, not a path: `sessions/../tokens/x` is a key called
// `../tokens/x` in the `sessions` map, and it is inside a `sessions/*` grant
// because the map is what the grant names. Nothing resolves it, so there is
// nothing to walk out of — which is exactly why storage names are normalised
// and cache keys are not.
func TestCacheHandlers_aKeyIsNotAPath(t *testing.T) {
	h := &CacheHandlers{}
	rec := httptest.NewRecorder()

	h.GetHandler(rec, requestWithGrant(t, "/v1/cache/get",
		GetRequest{DMap: "sessions", Key: "../tokens/user:1"}, "cache:key=sessions/*"))

	if rec.Code == http.StatusForbidden {
		t.Fatalf("a key in the granted map was refused for looking like a path: %s", rec.Body.String())
	}
}

// A credential with no selector is not narrowed. The scope gate has already
// decided whether it may reach the cache at all.
func TestCacheHandlers_anUnnarrowedGrantIsUntouched(t *testing.T) {
	h := &CacheHandlers{}
	rec := httptest.NewRecorder()

	h.GetHandler(rec, requestWithGrant(t, "/v1/cache/get",
		GetRequest{DMap: "anything", Key: "at-all"}, ""))

	if rec.Code == http.StatusForbidden {
		t.Fatalf("an unnarrowed grant was refused: %s", rec.Body.String())
	}
}

func TestCacheResourceName(t *testing.T) {
	for _, tc := range []struct{ dmap, key, want string }{
		{"sessions", "user:1", "sessions/user:1"},
		{"sessions", "", "sessions"},
		{" sessions ", " user:1 ", "sessions/user:1"},
	} {
		if got := cacheResourceName(tc.dmap, tc.key); got != tc.want {
			t.Errorf("cacheResourceName(%q, %q) = %q, want %q", tc.dmap, tc.key, got, tc.want)
		}
	}
}
