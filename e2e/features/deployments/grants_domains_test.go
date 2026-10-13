//go:build e2e_fleet

package deployments

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
)

const (
	pathDomainAdd    = "/v1/deployments/domains/add"
	pathDomainVerify = "/v1/deployments/domains/verify"
	pathDomainList   = "/v1/deployments/domains/list"
	pathDomainRemove = "/v1/deployments/domains/remove"
)

// TestDeployGrants_runtimeYesControlPlaneNo: an app is granted runtime or
// reader, never the control plane, and list shows the grant
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama app grants").
func TestDeployGrants_runtimeYesControlPlaneNo(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tn.deploy(t, "static", staticSite(t, "g"), "granted")
	tn.cli.MustOK(t, "app", "grants", "set", "granted", "runtime")
	if out := tn.cli.MustOK(t, "app", "grants", "list", "granted").Stdout; !strings.Contains(out, "runtime") {
		t.Fatalf("grants list does not show runtime:\n%s", out)
	}
	// A selector narrows a scope the role holds: runtime has scopes, reader has
	// none, so a reader with a selector is refused by design
	// (core/pkg/gateway/auth/grants.go).
	tn.cli.MustOK(t, "app", "grants", "set", "granted", "runtime", "--resource", "pubsub:topic=orders.*")
	if res, err := tn.cli.Run(t.Context(), "app", "grants", "set", "granted", "reader", "--resource", "pubsub:topic=orders.*"); err != nil || res.Exit == 0 {
		t.Errorf("a reader grant with a selector was accepted, but a reader holds no scope to narrow (%v)", err)
	}
	tn.cli.MustOK(t, "app", "grants", "set", "granted", "reader")
	for _, role := range []string{"admin", "owner", "superuser", ""} {
		if res, err := tn.cli.Run(t.Context(), "app", "grants", "set", "granted", role); err != nil || res.Exit == 0 {
			t.Errorf("an app was granted %q (%v)", role, err)
		}
		if r := tn.api(t, http.MethodPost, pathGrants, map[string]string{"name": "granted", "role": role}); r.Status == http.StatusOK {
			t.Errorf("POST grants with role %q answered 200", role)
		}
	}
	tn.api(t, http.MethodPost, pathGrants, map[string]string{"role": "runtime"}).Expect(t, http.StatusBadRequest)
	tn.api(t, http.MethodGet, pathGrants, nil).Expect(t, http.StatusOK)
	if help := tn.cli.MustOK(t, "app", "grants", "--help").Stdout; !strings.Contains(help, "list") || !strings.Contains(help, "set") {
		t.Errorf("orama app grants --help: %q", help)
	}
}

// TestDeployGrants_nameIsADeploymentOfTheNamespace: a grant names a deployment
// that exists in the caller's namespace and has a name a deployment can have; a
// name with '/' or ':' is 400 and one that is no deployment is 404, and neither
// leaves a grant behind (docs/whitepaper/technical-reference/vol1/14-authorization.md). A granted app
// lists with its role and selector, in the CLI's --json too.
func TestDeployGrants_nameIsADeploymentOfTheNamespace(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tn.deploy(t, "static", staticSite(t, "gn"), "named")
	out := tn.cli.MustOK(t, "app", "grants", "set", "named", "runtime", "--resource", "pubsub:topic=orders.*").Stdout
	if !strings.Contains(out, "named may now act as 'runtime'") {
		t.Errorf("grants set printed:\n%s", out)
	}
	var listed struct {
		Grants []struct {
			Deployment string `json:"deployment"`
			Role       string `json:"role"`
			Resource   string `json:"resource"`
		} `json:"grants"`
	}
	if err := json.Unmarshal([]byte(tn.cli.MustOK(t, "app", "grants", "list", "named", "--json").Stdout), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Grants) != 1 || listed.Grants[0].Role != "runtime" || listed.Grants[0].Resource != "pubsub:topic=orders.*" {
		t.Errorf("grants list --json: %+v", listed.Grants)
	}
	for _, name := range []string{"named/x", "other/named", "named:admin", "named/../x"} {
		tn.api(t, http.MethodPost, pathGrants, map[string]string{"name": name, "role": "runtime"}).Expect(t, http.StatusBadRequest)
	}
	tn.api(t, http.MethodPost, pathGrants, map[string]string{"name": "never-deployed", "role": "runtime"}).Expect(t, http.StatusNotFound)
	res, err := tn.cli.Run(t.Context(), "app", "grants", "set", "never-deployed", "runtime")
	if err != nil || res.Exit == 0 || !strings.Contains(strings.ToLower(res.Stdout+res.Stderr), "no such deployment") {
		t.Errorf("granting a deployment that does not exist: %v %+v", err, res)
	}
	var after struct {
		Grants []struct {
			Deployment string `json:"deployment"`
		} `json:"grants"`
	}
	if err := tn.api(t, http.MethodGet, pathGrants, nil).Expect(t, http.StatusOK).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if len(after.Grants) != 1 || after.Grants[0].Deployment != "named" {
		t.Errorf("a refused grant was recorded: %+v", after.Grants)
	}
}

// TestDeployDomains_addListVerifyRemove: a custom domain is registered with a
// verification token, listed, not served until verified, and removed; the
// network's own base domain and malformed names are refused
// (website/src/docs/developer/domains.mdx "Custom Domains"). Verification's success path
// needs a TXT record the test can publish, which features cannot.
func TestDeployDomains_addListVerifyRemove(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tn.deploy(t, "static", staticSite(t, "dom"), "domapp")
	domain := "e2e-" + tn.n.Name + ".example.org"
	out := tn.cli.MustOK(t, "domain", "add", domain, "--app", "domapp").Stdout
	if !strings.Contains(out, "_orama-verify") {
		t.Errorf("domain add printed no TXT record to create:\n%s", out)
	}
	t.Cleanup(func() {
		tenancy.Restore(t, tn.n.Client, http.MethodDelete, pathDomainRemove+"?domain="+domain, tn.admin, nil, http.StatusOK, http.StatusNotFound)
	})
	for _, args := range [][]string{{"domain", "list"}, {"domain", "list", "--app", "domapp"}} {
		if !strings.Contains(tn.cli.MustOK(t, args...).Stdout, domain) {
			t.Errorf("orama %s does not show %s", strings.Join(args, " "), domain)
		}
	}
	if res, err := tn.cli.Run(t.Context(), "domain", "verify", domain); err != nil || res.Exit == 0 {
		t.Errorf("verify succeeded with no TXT record published (%v)", err)
	}
	for _, bad := range []string{"x." + tn.f.State.BaseDomain, tn.f.State.BaseDomain, "not a domain", "-bad-.com", "a..b.com", domain} {
		if r := tn.api(t, http.MethodPost, pathDomainAdd, map[string]string{"deployment_name": "domapp", "domain": bad}); r.Status < http.StatusBadRequest {
			t.Errorf("domain %q was added: %d", bad, r.Status)
		}
	}
	tn.api(t, http.MethodPost, pathDomainAdd, map[string]string{"deployment_name": "missing", "domain": "e2e-other.example.org"}).Expect(t, http.StatusNotFound)
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if r := tn.api(t, m, pathDomainAdd, nil); r.Status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d", m, pathDomainAdd, r.Status)
		}
	}
	if r := tn.api(t, http.MethodGet, pathDomainRemove+"?domain="+domain, nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET remove answered %d; a GET must not delete", r.Status)
	}
	tn.api(t, http.MethodPost, pathDomainVerify, map[string]string{"domain": "never-added.example.org"}).Expect(t, http.StatusNotFound)
	tn.cli.MustOK(t, "domain", "remove", domain)
	if strings.Contains(string(tn.api(t, http.MethodGet, pathDomainList, nil).Expect(t, http.StatusOK).Body), domain) {
		t.Error("the removed domain is still listed")
	}
}
