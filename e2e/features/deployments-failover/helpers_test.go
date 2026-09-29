//go:build e2e_fleet

package deploymentsfailover

import (
	"context"
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

// Deployment routes (docs/API_SURFACE.md "Deployments").
const (
	pathDelete = "/v1/deployments/delete"
	// startBudget: the platform polls a new app's health path every 30s
	// (docs/DEPLOYMENT_GUIDE.md), and a server-side npm install may take 4 min.
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
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	admin := tenancy.OperatorMember(t, f, n, tenancy.RoleAdmin)
	return &tenant{f: f, n: n, cli: n.CLI, admin: tenancy.Cred{Bearer: admin.Token()}}
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

// app returns a client for the deployed app's own address.
func (tn *tenant) app(appURL string) *gw.Client { return tn.n.Client.WithBase(appURL) }

// instance is the systemd instance of a deployment, <namespace>-<name>.
func (tn *tenant) instance(name string) string { return tn.n.Name + "-" + name }

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
