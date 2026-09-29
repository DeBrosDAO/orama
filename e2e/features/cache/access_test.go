//go:build e2e_fleet

package cache

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// revocationBudget: a revocation reaches every gateway within the 10-second
// reload (docs/AUTH.md#revoking), plus the round trip.
const revocationBudget = 20 * time.Second

// cacheBodies is one valid body per cache route.
var cacheBodies = map[string]any{
	pathGet:    map[string]any{"dmap": "m", "key": "k"},
	pathPut:    map[string]any{"dmap": "m", "key": "k", "value": "v"},
	pathDelete: map[string]any{"dmap": "m", "key": "k"},
	pathMGet:   map[string]any{"dmap": "m", "keys": []string{"k"}},
	pathScan:   map[string]any{"dmap": "m"},
}

func TestCacheAuth_noCredentialRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for path, body := range cacheBodies {
		tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, path, tenancy.Cred{}, body), http.StatusUnauthorized, tenancy.CodeMissing)
	}
}

func TestCacheAuth_garbageCredentialRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.ExpectDenied(t, tenancy.Post(t, n.Client, pathPut, tenancy.Cred{Bearer: "x.y.z"}, cacheBodies[pathPut]), "garbage bearer")
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathPut, tenancy.Cred{APIKey: "orama_rk_notakey_0000"}, cacheBodies[pathPut]),
		http.StatusUnauthorized, tenancy.CodeInvalid)
	get(t, n.Client, tenancy.Owner(n), "m", "k").Expect(t, http.StatusNotFound)
}

// TestCacheAuth_rolesDecide: the runtime role holds the data plane, the
// reader role holds nothing (docs/CLI_REFERENCE.md "orama members").
func TestCacheAuth_rolesDecide(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	runtime := tenancy.Member(t, n, tenancy.RoleRuntime)
	reader := tenancy.Member(t, n, tenancy.RoleReader)
	put(t, n.Client, tenancy.Cred{Bearer: runtime.Token()}, "m", "k", "by-runtime", "").Expect(t, http.StatusOK)
	if got := mustGet(t, n.Client, tenancy.Cred{Bearer: runtime.Token()}, "m", "k"); got != "by-runtime" {
		t.Fatalf("runtime member read %v", got)
	}
	for path, body := range cacheBodies {
		tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, path, tenancy.Cred{Bearer: reader.Token()}, body), http.StatusForbidden, tenancy.CodeScope)
	}
	if got := mustGet(t, n.Client, tenancy.Owner(n), "m", "k"); got != "by-runtime" {
		t.Fatalf("a refused reader write changed the value to %v", got)
	}
}

// TestCacheAuth_keyGrantDecides: a key holds exactly its grants
// (docs/ARCHITECTURE.md "API Keys").
func TestCacheAuth_keyGrantDecides(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	withCache := tenancy.APIKey(t, n, "cache")
	pubsubOnly := tenancy.APIKey(t, n, "pubsub")
	put(t, n.Client, tenancy.Cred{APIKey: withCache}, "m", "k", 7.0, "").Expect(t, http.StatusOK)
	if got := mustGet(t, n.Client, tenancy.Cred{APIKey: withCache}, "m", "k"); got != 7.0 {
		t.Fatalf("cache key read %v", got)
	}
	tenancy.ExpectRefused(t, get(t, n.Client, tenancy.Cred{APIKey: pubsubOnly}, "m", "k"), http.StatusForbidden, tenancy.CodeScope)
	tenancy.ExpectRefused(t, put(t, n.Client, tenancy.Cred{APIKey: pubsubOnly}, "m", "k", 0, ""), http.StatusForbidden, tenancy.CodeScope)
}

// TestCacheAuth_revokedSessionStops: a logged-out member's access token stops
// working everywhere within the revocation reload (docs/AUTH.md#revoking).
func TestCacheAuth_revokedSessionStops(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	m := tenancy.Member(t, n, tenancy.RoleRuntime)
	put(t, n.Client, tenancy.Cred{Bearer: m.Token()}, "m", "k", "v", "").Expect(t, http.StatusOK)
	if _, err := m.Client.For(t).Logout(t.Context(), m.Token(), m.Session.RefreshToken, n.Name, false); err != nil {
		t.Fatal(err)
	}
	for _, nc := range tenancy.PerNode(t, harness.Fleet(t), n.Client) {
		eventually.Require(t, pollEvery, revocationBudget, nc.Node.Name+" to refuse the revoked token", func() (bool, error) {
			resp := get(t, nc.Client, tenancy.Cred{Bearer: m.Token()}, "m", "k")
			if resp.Status == http.StatusUnauthorized && resp.ErrorCode() == tenancy.CodeRevoked {
				return true, nil
			}
			return false, fmt.Errorf("HTTP %d %s", resp.Status, resp.ErrorCode())
		})
	}
}

// TestCacheIsolation_otherNamespaceRefused: B's credentials never read A's
// cache; B's key is refused by name (docs/SECURITY.md, NAMESPACE_MISMATCH).
func TestCacheIsolation_otherNamespaceRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	put(t, a.Client, tenancy.Owner(a), "secrets", "k", "only-a", "").Expect(t, http.StatusOK)
	tenancy.ExpectDenied(t, get(t, a.Client, tenancy.Owner(b), "secrets", "k"), "B's session at A's gateway")
	tenancy.ExpectRefused(t, get(t, a.Client, tenancy.Cred{APIKey: tenancy.APIKey(t, b, "cache")}, "secrets", "k"), http.StatusForbidden, tenancy.CodeMismatch)
	// The same map and key in B is B's own, empty.
	get(t, b.Client, tenancy.Owner(b), "secrets", "k").Expect(t, http.StatusNotFound)
	if keys := scanKeys(t, b, "secrets", ""); len(keys) != 0 {
		t.Fatalf("B's scan of the map name A uses returned %v", keys)
	}
}

func TestCacheIsolation_sameNamesDoNotCollide(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	put(t, a.Client, tenancy.Owner(a), "m", "k", "a", "").Expect(t, http.StatusOK)
	put(t, b.Client, tenancy.Owner(b), "m", "k", "b", "").Expect(t, http.StatusOK)
	tenancy.Post(t, b.Client, pathDelete, tenancy.Owner(b), map[string]any{"dmap": "m", "key": "k"}).Expect(t, http.StatusOK)
	if got := mustGet(t, a.Client, tenancy.Owner(a), "m", "k"); got != "a" {
		t.Fatalf("A's value is %v after B wrote and deleted the same name", got)
	}
}

// TestCacheAuth_keySelectorGrant: a member grant narrowed to cache:key=... is
// either enforced (only the selected map) or, while the gateway says
// "enforced": false, authorises nothing (core/pkg/gateway/members_routes.go).
// In no case may it reach a key outside the selector.
func TestCacheAuth_keySelectorGrant(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	sel := tenancy.SelectorMember(t, n, "cache:key=sessions/*")
	put(t, n.Client, tenancy.Owner(n), "sessions", "s1", "in", "").Expect(t, http.StatusOK)
	put(t, n.Client, tenancy.Owner(n), "other", "o1", "out", "").Expect(t, http.StatusOK)
	// Enforced, the handler refuses with a plain 403 (handlers/cache/authorize.go);
	// unenforced, the scope gate does: 403 either way.
	get(t, n.Client, tenancy.Cred{Bearer: sel.Token}, "other", "o1").Expect(t, http.StatusForbidden)
	resp := get(t, n.Client, tenancy.Cred{Bearer: sel.Token}, "sessions", "s1")
	switch {
	case sel.Enforced && resp.Status != http.StatusOK:
		t.Fatalf("an enforced selector refused its own map: %d %s", resp.Status, resp.Body)
	case !sel.Enforced && resp.Status == http.StatusOK:
		t.Fatalf("the gateway said the selector is not enforced, yet the grant read the cache: %s", resp.Body)
	}
	if sel.Enforced {
		var out struct{ Keys []string }
		if err := tenancy.Post(t, n.Client, pathScan, tenancy.Cred{Bearer: sel.Token}, map[string]any{"dmap": "other"}).Expect(t, http.StatusOK).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if len(out.Keys) != 0 {
			t.Fatalf("a sessions/* selector scanned keys of another map: %v", out.Keys)
		}
	}
}
