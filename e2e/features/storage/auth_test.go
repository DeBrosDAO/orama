//go:build e2e_fleet

package storage

import (
	"net/http"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// storageRoutes are the routes that require a logged-in user
// (core/pkg/gateway/route_policy.go dataPlane storage WalletToken).
func storageRoutes(cid string) map[string]gw.Req {
	return map[string]gw.Req{
		"upload": {Method: http.MethodPost, Path: pathUpload, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"data":"aGk="}`)},
		"pin":    {Method: http.MethodPost, Path: pathPin, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"cid":"` + cid + `"}`)},
		"get":    {Path: pathGet + cid},
		"status": {Path: pathStatus + cid},
	}
}

// TestAuth_noCredential: every storage route is 401 AUTH_MISSING anonymously.
func TestAuth_noCredential(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	u := upload(t, n.Client, tenancy.Owner(n), "a.bin", []byte("a"))
	for name, req := range storageRoutes(u.Cid) {
		t.Run(name, func(t *testing.T) {
			tenancy.ExpectRefused(t, n.Client.MustSend(t, req), http.StatusUnauthorized, tenancy.CodeMissing)
		})
	}
	r := n.Client.MustSend(t, gw.Req{Method: http.MethodDelete, Path: pathUnpin + u.Cid})
	tenancy.ExpectRefused(t, r, http.StatusUnauthorized, tenancy.CodeMissing)
}

// TestAuth_apiKeyAloneRefused: a storage-scoped key reaches no storage route
// without a logged-in user: 401 USER_JWT_REQUIRED (docs/AUTH.md; the layer
// that makes an extracted runtime key inert).
func TestAuth_apiKeyAloneRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	u := upload(t, n.Client, tenancy.Owner(n), "b.bin", []byte("b"))
	key := tenancy.APIKey(t, n, "storage")
	for name, req := range storageRoutes(u.Cid) {
		req.APIKey = key
		r := n.Client.MustSend(t, req)
		if r.Status != http.StatusUnauthorized || r.ErrorCode() != codeJWTNeeded {
			t.Errorf("%s with a bare key: want 401 %s, got %d %s", name, codeJWTNeeded, r.Status, r.ErrorCode())
		}
	}
	r := n.Client.MustSend(t, gw.Req{Method: http.MethodDelete, Path: pathUnpin + u.Cid, APIKey: key})
	if r.Status != http.StatusUnauthorized {
		t.Errorf("unpin with a bare key: want 401, got %d: %.200s", r.Status, r.Body)
	}
}

// TestAuth_exchangedKeyTokenUnpinsOnly: a token exchanged from a storage key
// may unpin (DELETE, bugboard #151: a userless reclaim) and nothing else.
func TestAuth_exchangedKeyTokenUnpinsOnly(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	u := upload(t, n.Client, tenancy.Owner(n), "c.bin", []byte("c"))
	s, _, err := n.Client.For(t).Token(t.Context(), tenancy.APIKey(t, n, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range storageRoutes(u.Cid) {
		req.Bearer = s.AccessToken
		if r := n.Client.MustSend(t, req); r.Status != http.StatusUnauthorized || r.ErrorCode() != codeJWTNeeded {
			t.Errorf("%s with an exchanged-key token: want 401 %s, got %d", name, codeJWTNeeded, r.Status)
		}
	}
	r, body := unpin(t, n.Client, tenancy.Cred{Bearer: s.AccessToken}, u.Cid, false)
	if r.Status != http.StatusOK || body["status"] != "ok" {
		t.Errorf("unpin with an exchanged-key token: %d %.200s", r.Status, r.Body)
	}
}

// TestAuth_roles: a runtime member uploads and reads; a reader holds no
// storage grant (403 INSUFFICIENT_SCOPE); a key without the storage scope is
// refused before the token check.
func TestAuth_roles(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	runtime := tenancy.Member(t, n, tenancy.RoleRuntime)
	data := []byte("runtime-data")
	u := upload(t, n.Client, tenancy.Cred{Bearer: runtime.Token()}, "rt.bin", data)
	waitContent(t, n.Client, tenancy.Cred{Bearer: runtime.Token()}, u.Cid, data)
	reader := tenancy.Member(t, n, tenancy.RoleReader)
	tenancy.ExpectRefused(t, uploadRaw(t, n.Client, tenancy.Cred{Bearer: reader.Token()}, "r.bin", data),
		http.StatusForbidden, tenancy.CodeScope)
	cacheKey := tenancy.APIKey(t, n, "cache")
	tenancy.ExpectRefused(t, uploadRaw(t, n.Client, tenancy.Cred{APIKey: cacheKey}, "k.bin", data),
		http.StatusForbidden, tenancy.CodeScope)
}

// TestAuth_invalidAndForeignCredentials: garbage and another namespace's
// session are refused, and never store anything.
func TestAuth_invalidAndForeignCredentials(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	a, b := nss[0], nss[1]
	for name, who := range map[string]tenancy.Cred{
		"garbage bearer":    {Bearer: "x.y.z"},
		"garbage key":       {APIKey: "orama_sk_nope_nope"},
		"foreign namespace": tenancy.Owner(b),
	} {
		r := uploadRaw(t, a.Client, who, "x.bin", []byte("x"))
		tenancy.ExpectDenied(t, r, name)
	}
}
