//go:build e2e_fleet

package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Deployment routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Deployments").
const (
	pathGet      = "/v1/deployments/get"
	pathList     = "/v1/deployments/list"
	pathDelete   = "/v1/deployments/delete"
	pathVersions = "/v1/deployments/versions"
	pathRollback = "/v1/deployments/rollback"
	pathLogs     = "/v1/deployments/logs"
	pathStats    = "/v1/deployments/stats"
	pathEvents   = "/v1/deployments/events"
	pathEnv      = "/v1/deployments/env"
	pathEnvSet   = "/v1/deployments/env/set"
	pathGrants   = "/v1/deployments/grants"
	// startBudget: the platform polls a new app's health path every 30s
	// (website/src/docs/developer/deployments.mdx), and a server-side npm install may take 4 min.
	startBudget = 6 * time.Minute
	pollEvery   = 3 * time.Second
	// cleanupBudget bounds one cleanup call.
	cleanupBudget = 2 * time.Minute
)

// urlLine is how `orama deploy` prints the app's address ("  • https://...").
var urlLine = regexp.MustCompile(`•\s+(https://\S+)`)

// tenant is a namespace driven as its operator (CLI) with an admin member for
// the HTTP calls the CLI does not make.
type tenant struct {
	f     *fleet.Fleet
	n     *ns.Namespace
	cli   *oramacli.Runner
	admin tenancy.Cred
}

func newTenant(t testing.TB) *tenant {
	t.Helper()
	return newTenants(t, 1)[0]
}

// newTenants creates count tenants with their namespaces reserved at once: a
// test that took them one by one would hold a slot while it waited for the
// next, which the harness refuses.
func newTenants(t testing.TB, count int) []*tenant {
	t.Helper()
	f := harness.Fleet(t)
	out := make([]*tenant, count)
	for i, n := range tenancy.Namespaces(t, f, count, ns.Options{Via: ns.ViaOperator}) {
		admin := tenancy.OperatorMember(t, f, n, tenancy.RoleAdmin)
		out[i] = &tenant{f: f, n: n, cli: n.CLI, admin: tenancy.Cred{Bearer: admin.Token()}}
	}
	return out
}

// deploy runs `orama deploy <runtime> <dir> --name <name> <extra...>`,
// registers the app's deletion, and returns its URL.
func (tn *tenant) deploy(t testing.TB, runtime, dir, name string, extra ...string) string {
	t.Helper()
	args := append([]string{"deploy", runtime, dir, "--name", name}, extra...)
	res := tn.cli.MustOK(t, args...)
	if !contains(extra, "--update") {
		t.Cleanup(func() { tn.deleteApp(t, name) })
	}
	m := urlLine.FindStringSubmatch(res.Stdout)
	if m == nil {
		t.Fatalf("orama deploy printed no URL:\n%s", res.Stdout)
	}
	return m[1]
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// deleteApp deletes name over HTTP (the CLI's delete asks for a typed "y",
// which the harness cannot give); already gone is fine.
func (tn *tenant) deleteApp(t testing.TB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	r, err := tn.n.Client.Send(ctx, gw.Req{Method: http.MethodDelete, Path: pathDelete + "?name=" + url.QueryEscape(name), Bearer: tn.admin.Bearer})
	if err != nil || (r.Status != http.StatusOK && r.Status != http.StatusNotFound) {
		t.Errorf("cleanup: deleting app %s: %v %v", name, err, r)
	}
}

// api is an HTTP call to the namespace gateway as the admin member.
func (tn *tenant) api(t testing.TB, method, path string, body any) *gw.Response {
	t.Helper()
	return tenancy.Send(t, tn.n.Client, method, path, tn.admin, body)
}

// app returns a client for the deployed app's own address.
func (tn *tenant) app(appURL string) *gw.Client { return tn.n.Client.WithBase(appURL) }

// instance is the systemd instance of a deployment, <namespace>-<name>.
func (tn *tenant) instance(name string) string { return tn.n.Name + "-" + name }

// answer is the probe app's reply.
type answer struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func probe(t testing.TB, c *gw.Client, path string, q url.Values) answer {
	t.Helper()
	full := path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	var a answer
	if err := c.MustSend(t, gw.Req{Path: full}).Expect(t, http.StatusOK).Decode(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

// serving waits until the app answers path with 200 and, when want is not
// empty, a body containing want.
func serving(t testing.TB, c *gw.Client, path, want string) {
	t.Helper()
	eventually.Require(t, pollEvery, startBudget, "the app to serve "+path, func() (bool, error) {
		r, err := c.Send(t.Context(), gw.Req{Path: path})
		if err != nil {
			return false, err
		}
		if r.Status != http.StatusOK || !strings.Contains(string(r.Body), want) {
			return false, fmt.Errorf("HTTP %d %.120s", r.Status, r.Body)
		}
		return true, nil
	})
}

// unitNodes are the core nodes where unit is active.
func unitNodes(t testing.TB, f *fleet.Fleet, unit string) []fleet.Node {
	t.Helper()
	var out []fleet.Node
	for _, node := range f.State.Nodes {
		if f.Unit(t, node, unit) == "active" {
			out = append(out, node)
		}
	}
	return out
}

// decode is a JSON decode that fails the test.
func decode(t testing.TB, r *gw.Response, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("HTTP %d is not the expected JSON: %v %.200s", r.Status, err, r.Body)
	}
}
