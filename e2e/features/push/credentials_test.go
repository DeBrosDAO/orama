//go:build e2e_fleet

package push

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// namespacesDir holds each namespace's data on a node (tenancy.NamespacesDir).
const namespacesDir = tenancy.NamespacesDir

// TestCredentials_apnsLifecycle: PUT stores, GET and the summary report only
// has_* booleans, DELETE is idempotent, and the p8 key never appears on a
// node's disk in plaintext (website/src/docs/developer/push-notifications.mdx#step-3--store-credentials-via-the-api).
func TestCredentials_apnsLifecycle(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	owner := tenancy.Owner(n)
	key := p8Key(t)
	body := strings.Split(key, "\n")[2] // a line of the key's base64 body
	if configured := configuredProviders(t, n, owner); slices.Contains(configured, "apns") {
		t.Fatalf("a new namespace already has apns configured: %v", configured)
	}
	r := put(t, n.Client, pathCredsAPNs, owner, apnsCreds(t, key)).Expect(t, http.StatusOK)
	for _, resp := range [][]byte{r.Body, tenancy.Get(t, n.Client, pathCredsAPNs, owner).Expect(t, http.StatusOK).Body} {
		s := string(resp)
		if strings.Contains(s, body) || strings.Contains(s, "PRIVATE KEY") || !strings.Contains(s, `"has_p8_key":true`) {
			t.Errorf("a credential answer carries the key or lacks has_p8_key: %.400s", s)
		}
	}
	// "supported" always lists apns; only "configured" says it is stored.
	if configured := configuredProviders(t, n, owner); !slices.Contains(configured, "apns") {
		t.Errorf("the summary's configured list is %v after the PUT, want apns in it", configured)
	}
	for _, node := range f.State.Nodes {
		out := f.Exec(t, node, "grep -rlF -- "+fleet.ShellQuote(body)+" "+namespacesDir+"/"+n.Name+" 2>/dev/null | head -3")
		if strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s: the p8 key is stored in plaintext in %s", node.Name, out.Stdout)
		}
	}
	del(t, n.Client, pathCredsAPNs, owner, nil).Expect(t, http.StatusOK)
	del(t, n.Client, pathCredsAPNs, owner, nil).Expect(t, http.StatusOK)
	if s := string(tenancy.Get(t, n.Client, pathCredsAPNs, owner).Body); strings.Contains(s, `"has_p8_key":true`) {
		t.Errorf("a deleted credential still reads configured: %s", s)
	}
	if configured := configuredProviders(t, n, owner); slices.Contains(configured, "apns") {
		t.Errorf("the summary's configured list is %v after the DELETE, want no apns", configured)
	}
}

// configuredProviders is the "configured" list of GET
// /v1/namespace/push-credentials (its "supported" list names every provider
// the gateway knows, stored or not).
func configuredProviders(t testing.TB, n *ns.Namespace, who tenancy.Cred) []string {
	t.Helper()
	var summary struct {
		Configured []string `json:"configured"`
	}
	if err := tenancy.Get(t, n.Client, pathCreds, who).Expect(t, http.StatusOK).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	return summary.Configured
}

// TestCredentials_validation: APNs and ntfy records are validated at PUT;
// an unknown provider, an internal ntfy base_url, a body over 32 KiB and an
// empty body are refused (website/src/docs/developer/push-notifications.mdx; core push/url_guard.go).
func TestCredentials_validation(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	key := p8Key(t)
	bad := func(mut func(map[string]any)) map[string]any { c := apnsCreds(t, key); mut(c); return c }
	cases := map[string]struct {
		path string
		body any
	}{
		"apns team_id 9 chars":  {pathCredsAPNs, bad(func(c map[string]any) { c["team_id"] = "123456789" })},
		"apns bundle not rdns":  {pathCredsAPNs, bad(func(c map[string]any) { c["bundle_id"] = "myapp" })},
		"apns key not PEM":      {pathCredsAPNs, bad(func(c map[string]any) { c["p8_key"] = "not a key" })},
		"apns env staging":      {pathCredsAPNs, bad(func(c map[string]any) { c["environment"] = "staging" })},
		"ntfy opaque no secret": {pathCredsNtfy, map[string]any{"topic_mode": "opaque"}},
		"ntfy bogus mode":       {pathCredsNtfy, map[string]any{"topic_mode": "bogus"}},
		"ntfy loopback":         {pathCredsNtfy, map[string]any{"base_url": "http://127.0.0.1:10109", "topic_mode": "path"}},
		"ntfy overlay":          {pathCredsNtfy, map[string]any{"base_url": "http://10.0.0.1:10109", "topic_mode": "path"}},
		"ntfy metadata":         {pathCredsNtfy, map[string]any{"base_url": "http://169.254.169.254/", "topic_mode": "path"}},
		"ntfy decimal ip":       {pathCredsNtfy, map[string]any{"base_url": "http://2130706433/", "topic_mode": "path"}},
		"ntfy scheme":           {pathCredsNtfy, map[string]any{"base_url": "ftp://push.example.com", "topic_mode": "path"}},
		"ntfy name to loopback": {pathCredsNtfy, map[string]any{"base_url": "http://localtest.me/", "topic_mode": "path"}},
		"unknown provider":      {pathCreds + "/fcm", map[string]any{"x": 1}},
		"empty body":            {pathCredsAPNs, []byte{}},
		"not JSON":              {pathCredsAPNs, []byte("team_id=x")},
		"over 32 KiB":           {pathCredsAPNs, bad(func(c map[string]any) { c["p8_key"] = strings.Repeat("A", maxCredsBody) })},
	}
	for name, tc := range cases {
		status(t, name, put(t, n.Client, tc.path, owner, tc.body), http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	}
	put(t, n.Client, pathCredsNtfy, owner, map[string]any{"topic_mode": "path"}).Expect(t, http.StatusOK)
	t.Cleanup(func() { tenancy.Restore(t, n.Client, http.MethodDelete, pathCredsNtfy, owner, nil, http.StatusOK) })
}

// TestCredentials_whoMayManage: credentials are the owner's secrets:write;
// a runtime member, a reader, anonymous callers and another namespace are
// refused, and none of them can read what is stored.
func TestCredentials_whoMayManage(t *testing.T) {
	t.Parallel()
	nss := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	n, other := nss[0], nss[1]
	put(t, n.Client, pathCredsAPNs, tenancy.Owner(n), apnsCreds(t, p8Key(t))).Expect(t, http.StatusOK)
	t.Cleanup(func() {
		tenancy.Restore(t, n.Client, http.MethodDelete, pathCredsAPNs, tenancy.Owner(n), nil, http.StatusOK)
	})
	runtime := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()}
	reader := tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()}
	for name, who := range map[string]tenancy.Cred{"runtime": runtime, "reader": reader} {
		t.Run(name, func(t *testing.T) {
			tenancy.ExpectRefused(t, put(t, n.Client, pathCredsAPNs, who, apnsCreds(t, p8Key(t))), http.StatusForbidden, tenancy.CodeScope)
			tenancy.ExpectRefused(t, tenancy.Get(t, n.Client, pathCredsAPNs, who), http.StatusForbidden, tenancy.CodeScope)
			tenancy.ExpectRefused(t, del(t, n.Client, pathCredsAPNs, who, nil), http.StatusForbidden, tenancy.CodeScope)
		})
	}
	tenancy.ExpectRefused(t, tenancy.Get(t, n.Client, pathCreds, tenancy.Cred{}), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.ExpectDenied(t, del(t, n.Client, pathCredsAPNs, tenancy.Owner(other), nil), "another namespace deleting credentials")
	if s := string(tenancy.Get(t, n.Client, pathCredsAPNs, tenancy.Owner(n)).Body); !strings.Contains(s, `"has_p8_key":true`) {
		t.Errorf("a refused caller changed the stored credential: %s", s)
	}
}

// TestLegacyConfig_boundsAndRedaction: /v1/push/config takes at most 16 KiB,
// refuses an internal ntfy_base_url, and never returns the tokens it stores.
func TestLegacyConfig_boundsAndRedaction(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	owner := tenancy.Owner(n)
	token := "expo-" + randomTopic(t)
	put(t, n.Client, pathConfig, owner, map[string]any{"expo_access_token": token}).Expect(t, http.StatusOK)
	t.Cleanup(func() { tenancy.Restore(t, n.Client, http.MethodDelete, pathConfig, owner, nil, http.StatusOK) })
	s := string(tenancy.Get(t, n.Client, pathConfig, owner).Expect(t, http.StatusOK).Body)
	if strings.Contains(s, token) {
		t.Errorf("GET /v1/push/config returned the stored token")
	}
	if !strings.Contains(s, `"has_expo_access_token":true`) {
		t.Errorf("GET /v1/push/config does not say a token is stored, so an empty answer would pass: %.300s", s)
	}
	status(t, "over 16 KiB", put(t, n.Client, pathConfig, owner, map[string]any{"expo_access_token": strings.Repeat("x", maxConfigBody)}),
		http.StatusBadRequest, http.StatusRequestEntityTooLarge)
	status(t, "loopback base url", put(t, n.Client, pathConfig, owner, map[string]any{"ntfy_base_url": "http://127.0.0.1:10109"}), http.StatusBadRequest)
}
