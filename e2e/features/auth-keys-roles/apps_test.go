//go:build e2e_fleet

package authkeysroles

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const pathRenew = "/v1/auth/renew"

type appGrant struct {
	Deployment string `json:"deployment"`
	Role       string `json:"role"`
	Resource   string `json:"resource"`
}

// TestAppGrants_dataPlaneOnly: a deployment may be granted runtime or reader,
// narrowed or not; it may never be granted the control plane — admin, owner
// or developer (docs/whitepaper/technical-reference/vol1/14-authorization.md#workloads-get-identity-and-no-grants-by-default: "A deployment cannot be
// granted the control plane"). Every refusal is a client error.
func TestAppGrants_dataPlaneOnly(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	owner := n.Owner.Token()
	for _, g := range []map[string]string{
		{"name": "e2e-api", "role": roleRuntime},
		{"name": "e2e-worker", "role": roleRuntime, "resource": "pubsub:topic=jobs.*"},
		{"name": "e2e-static", "role": roleReader},
	} {
		send(t, c, http.MethodPost, pathGrants, owner, g).Expect(t, http.StatusOK)
	}
	var listed struct {
		Grants []appGrant `json:"grants"`
	}
	if err := send(t, c, http.MethodGet, pathGrants, owner, nil).Expect(t, http.StatusOK).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Grants) < 3 {
		t.Errorf("grants list: %+v", listed.Grants)
	}
	for _, g := range []map[string]string{
		{"name": "e2e-api", "role": roleAdmin},
		{"name": "e2e-api", "role": roleOwner},
		{"name": "e2e-api", "role": roleDev},
		{"name": "e2e-api", "role": roleRuntime, "resource": "db:table=posts"},
		{"name": "e2e-api", "role": "wizard"},
		{"role": roleRuntime},
	} {
		r := send(t, c, http.MethodPost, pathGrants, owner, g)
		if r.Status < http.StatusBadRequest || r.Status >= http.StatusInternalServerError {
			t.Errorf("granting %v: want a 4xx refusal, got %d %s", g, r.Status, r.Body)
		}
	}
	if r := send(t, c, http.MethodPut, pathGrants, owner, map[string]string{"name": "x", "role": roleRuntime}); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("PUT grants: want 405, got %d", r.Status)
	}
}

// TestAppGrants_cli: `orama app grants set/list` as the operator, with --json.
func TestAppGrants_cli(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	out := n.CLI.MustOK(t, "app", "grants", "set", "e2e-cli-app", roleRuntime, "--resource", "pubsub:topic=orders.*").Stdout
	if !strings.Contains(out, "e2e-cli-app may now act as 'runtime'") {
		t.Errorf("grants set printed:\n%s", out)
	}
	var listed struct {
		Grants []appGrant `json:"grants"`
	}
	if err := decodeJSON(n.CLI.MustOK(t, "app", "grants", "list", "e2e-cli-app", "--json").Stdout, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Grants) != 1 || listed.Grants[0].Role != roleRuntime || listed.Grants[0].Resource != "pubsub:topic=orders.*" {
		t.Errorf("grants list --json: %+v", listed.Grants)
	}
	if res := runCLI(t, n.CLI, "app", "grants", "set", "e2e-cli-app", roleAdmin); res.Exit == 0 {
		t.Error("the CLI granted a deployment admin")
	}
	for _, args := range [][]string{{"app", "grants"}, {"app"}} {
		if out := n.CLI.MustOK(t, args...).Stdout; !strings.Contains(out, "grants") && !strings.Contains(out, "list") {
			t.Errorf("`orama %s` does not list its subcommands:\n%s", strings.Join(args, " "), out)
		}
	}
}

// TestRenew_onlyAWorkloadRenewsItself: a user session is renewed by its
// refresh token, never at /v1/auth/renew; neither can a key or nothing
// (docs/whitepaper/technical-reference/vol1/14-authorization.md#workloads-get-identity-and-no-grants-by-default). These are the refusals only: the
// positive path, a deployed workload renewing its own token, needs a running
// app and is asserted by features/reference-apps.
func TestRenew_onlyAWorkloadRenewsItself(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := ns.New(t, f, ns.Options{})
	c := harness.GW(t)
	key := mintKey(t, n, map[string]any{"scope": "cache"}).APIKey
	exchanged, _, err := c.For(t).Token(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	for name, bearer := range map[string]string{"user session": n.Owner.Token(), "exchanged key token": exchanged.AccessToken} {
		if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathRenew, Bearer: bearer}); r.Status != http.StatusForbidden {
			t.Errorf("%s at /v1/auth/renew: want 403, got %d %s", name, r.Status, r.Body)
		}
	}
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathRenew}); r.Status != http.StatusUnauthorized {
		t.Errorf("no credential at /v1/auth/renew: want 401, got %d", r.Status)
	}
	if r := c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathRenew, Bearer: key}); r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
		t.Errorf("a bare key at /v1/auth/renew: want 401/403, got %d", r.Status)
	}
}
