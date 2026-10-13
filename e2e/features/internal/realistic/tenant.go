//go:build e2e_fleet

package realistic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
)

const (
	// StartBudget: the platform polls a new app's health path every 30s and a
	// server-side npm install may take minutes (website/src/docs/developer/deployments.mdx).
	StartBudget = 8 * time.Minute
	// PollEvery paces waits on apps, functions and triggers.
	PollEvery = 3 * time.Second
	// cleanupBudget bounds one cleanup call.
	cleanupBudget = 2 * time.Minute
	// pathDeploymentDelete deletes an app (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Deployments").
	pathDeploymentDelete = "/v1/deployments/delete"
	// exitNotFound is the CLI's "not found" exit code.
	exitNotFound = 4
)

// deployURL is how `orama deploy` prints the app's address ("  • https://...").
var deployURL = regexp.MustCompile(`•\s+(https://\S+)`)

// Tenant is a customer: a namespace created by the operator's CLI (the
// customer's own signed-in CLI), an admin member for the HTTP calls the CLI
// does not make, and the namespace gateway.
type Tenant struct {
	F *fleet.Fleet
	N *ns.Namespace
	// admin is the namespace's admin member. Its token is read at each use,
	// never kept: a soak or a chaos package outlives one access token, and a
	// copy taken at creation answered 401 to the cleanup an hour later.
	admin *gw.User
	// C is the namespace gateway, https://ns-<name>.<base>.
	C *gw.Client
}

// AdminToken is the admin member's current access token, refreshed when it is
// close to expiring (gw.User.Token).
func (tn *Tenant) AdminToken() string { return tn.admin.Token() }

// NewTenant creates the namespace within the package's namespace budget.
func NewTenant(t testing.TB) *Tenant {
	t.Helper()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	admin := tenancy.OperatorMember(t, f, n, tenancy.RoleAdmin)
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	return &Tenant{F: f, N: n, admin: admin, C: c}
}

// Deploy runs `orama deploy <runtime> <dir> --name <name> <extra...>` as the
// customer and returns the app's URL. A first deploy registers the app's
// deletion.
func (tn *Tenant) Deploy(t testing.TB, runtime, dir, name string, extra ...string) string {
	t.Helper()
	args := append([]string{"deploy", runtime, dir, "--name", name}, extra...)
	res := tn.N.CLI.MustOK(t, args...)
	update := false
	for _, a := range extra {
		update = update || a == "--update"
	}
	if !update {
		t.Cleanup(func() { tn.deleteApp(t, name) })
	}
	m := deployURL.FindStringSubmatch(res.Stdout)
	switch {
	case m != nil:
		return m[1]
	case update:
		return "" // an update keeps the address the first deploy printed
	}
	t.Fatalf("orama deploy %s printed no URL:\n%s", runtime, res.Stdout)
	return ""
}

// deleteApp deletes name over HTTP (the CLI's delete asks for a typed
// confirmation); already gone is fine.
func (tn *Tenant) deleteApp(t testing.TB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	r, err := tn.C.Send(ctx, gw.Req{Method: http.MethodDelete, Path: pathDeploymentDelete + "?name=" + url.QueryEscape(name), Bearer: tn.AdminToken()})
	if err != nil || (r.Status != http.StatusOK && r.Status != http.StatusNotFound) {
		t.Errorf("cleanup: deleting app %s: %v %v", name, err, r)
	}
}

// Grant gives a deployed app a role with `orama app grants set`.
func (tn *Tenant) Grant(t testing.TB, app, role string) {
	t.Helper()
	tn.N.CLI.MustOK(t, "app", "grants", "set", app, role)
}

// App is a client for a deployed app's own address.
func (tn *Tenant) App(appURL string) *gw.Client { return tn.C.WithBase(appURL) }

// Serving waits until c answers path with 200 and a body containing want.
func Serving(t testing.TB, c *gw.Client, path, want string) {
	t.Helper()
	eventually.Require(t, PollEvery, StartBudget, "the app to serve "+path+" from "+c.BaseURL+pinNote(c), func() (bool, error) {
		r, err := c.Send(t.Context(), gw.Req{Path: path})
		if err != nil {
			return false, err
		}
		if r.Status != http.StatusOK || !strings.Contains(string(r.Body), want) {
			return false, fmt.Errorf("HTTP %d %.160s", r.Status, r.Body)
		}
		return true, nil
	})
}

func pinNote(c *gw.Client) string {
	if ip := c.PinnedIP(); ip != "" {
		return " (node " + ip + ")"
	}
	return ""
}

// EveryNodeServes waits until each core node serves path by the app's name.
func (tn *Tenant) EveryNodeServes(t testing.TB, appURL, path, want string) {
	t.Helper()
	for _, node := range tn.F.State.Nodes {
		Serving(t, tn.App(appURL).PinTo(node.PublicIP), path, want)
	}
}

// RequireTinyGo skips (not covered) when `orama function build` cannot run.
func RequireTinyGo(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("tinygo"); err != nil {
		harness.SkipNotApplicable(t, "tinygo is not on the runner's PATH; `orama function deploy` compiles the reference functions with it")
	}
}

// DeployFunction deploys the reference WASM bundle as name with APP_ROLE
// role (public or private) through `orama function deploy`, and deletes it at
// cleanup.
func (tn *Tenant) DeployFunction(t testing.TB, name, role string, public bool) {
	t.Helper()
	dir := CopyApp(t, AppWASM, nil, nil)
	yaml := fmt.Sprintf("name: %s\npublic: %t\nmemory: 64\ntimeout: 30\nenv:\n  APP_ROLE: %s\n", name, public, role)
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(yaml), appFileMode); err != nil {
		t.Fatal(err)
	}
	tn.N.CLI.MustOK(t, "function", "deploy", dir)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		res, err := tn.N.CLI.Run(ctx, "function", "delete", name, "--force")
		if err != nil || (res.Exit != 0 && res.Exit != exitNotFound) {
			t.Errorf("cleanup: deleting function %s: %v %s", name, err, res.Stderr)
		}
	})
}

// Invoke calls fn on c as bearer and decodes a 200 reply into a map.
func Invoke(ctx context.Context, c *gw.Client, fn, bearer string, body any) (map[string]any, error) {
	var out map[string]any
	_, err := c.JSON(ctx, http.MethodPost, "/v1/functions/"+url.PathEscape(fn)+"/invoke", bearer, body, &out)
	if err != nil {
		return nil, err
	}
	if msg, _ := out["error"].(string); msg != "" {
		return out, fmt.Errorf("function %s answered an error: %s", fn, msg)
	}
	return out, nil
}

// Rows reads the "rows" of a db_query_v2 envelope a function returned under
// "result".
func Rows(out map[string]any) []map[string]any {
	res, _ := out["result"].(map[string]any)
	raw, _ := json.Marshal(res["rows"])
	var rows []map[string]any
	_ = json.Unmarshal(raw, &rows)
	return rows
}
