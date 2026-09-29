//go:build e2e_fleet

package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
	"github.com/DeBrosOfficial/network/e2e/harness/wallet"
)

const (
	fixtureDir   = "testdata/svcfn"
	badImportDir = "testdata/badimport"
	fixturePerm  = 0o644
	dirPerm      = 0o755
	cleanupLimit = 2 * time.Minute
	pollEvery    = 2 * time.Second
	roleAdmin    = "admin"
	roleRuntime  = "runtime"
	// Error envelope codes (core/pkg/httputil/rpc_error.go).
	codeRateLimited = "RATE_LIMITED"
	codeExecFailed  = "FUNCTION_EXECUTION_FAILED"
	codeNotFound    = "NOT_FOUND"
	codeMismatch    = "NAMESPACE_MISMATCH"
)

// fixture is a namespace created by the operator's CLI, with an admin and a
// runtime member signed in for the HTTP side.
type fixture struct {
	f       *fleet.Fleet
	n       *ns.Namespace
	c       *gw.Client // the namespace gateway
	admin   string     // an admin member's token
	runtime string     // a runtime member's token
}

// setup skips when tinygo is missing (`orama function build/deploy` needs
// it), creates the namespace and signs members in.
func setup(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("tinygo"); err != nil {
		harness.SkipNotApplicable(t, "tinygo is not on the runner's PATH; `orama function build/deploy` compiles with it")
	}
	f := harness.Fleet(t)
	tenancy.Reserve(t, harness.Fleet(t), 1)
	n := ns.New(t, f, ns.Options{Via: ns.ViaOperator})
	c := harness.GW(t).WithBase(gw.NamespaceURL(f.State, n.Name))
	return &fixture{f: f, n: n, c: c, admin: member(t, n, c, roleAdmin), runtime: member(t, n, c, roleRuntime)}
}

// member adds a fresh wallet with role through the CLI and signs it in.
func member(t *testing.T, n *ns.Namespace, c *gw.Client, role string) string {
	t.Helper()
	w, err := wallet.NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	n.CLI.MustOK(t, "members", "add", w.Address(), "--role", role)
	s, err := c.For(t).SignIn(t.Context(), w, n.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.AccessToken
}

// fnSpec is one deployment of a fixture.
type fnSpec struct {
	name   string
	src    string // fixtureDir or badImportDir
	yaml   string // extra function.yaml lines
	public bool
}

// deploy writes the fixture with spec's function.yaml into a scratch dir and
// deploys it with the CLI; the cleanup deletes the function.
func deploy(t *testing.T, fx *fixture, spec fnSpec) string {
	t.Helper()
	dir := writeFixture(t, spec)
	fx.n.CLI.MustOK(t, "function", "deploy", dir)
	t.Cleanup(func() { deleteFn(t, fx.n.CLI, spec.name) })
	return dir
}

func writeFixture(t *testing.T, spec fnSpec) string {
	t.Helper()
	src := spec.src
	if src == "" {
		src = fixtureDir
	}
	dir := filepath.Join(t.TempDir(), spec.name)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("fixture %s: %v", src, err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, fixturePerm); err != nil {
			t.Fatal(err)
		}
	}
	pub := "false"
	if spec.public {
		pub = "true"
	}
	yaml := "name: " + spec.name + "\npublic: " + pub + "\n" + spec.yaml
	if err := os.WriteFile(filepath.Join(dir, "function.yaml"), []byte(yaml), fixturePerm); err != nil {
		t.Fatal(err)
	}
	return dir
}

// deleteFn deletes name at cleanup; "not found" means a test already did.
func deleteFn(t *testing.T, cli *oramacli.Runner, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupLimit)
	defer cancel()
	res, err := cli.Run(ctx, "function", "delete", name, "--force")
	if err != nil || (res.Exit != 0 && res.Exit != 4) {
		t.Errorf("cleanup: failed to delete function %s: %v %s", name, err, res.Stderr)
	}
}

// invoke POSTs body to fn on the namespace gateway as bearer.
func invoke(t testing.TB, c *gw.Client, fn, bearer string, body any) *gw.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: "/v1/functions/" + url.PathEscape(fn) + "/invoke",
		Bearer: bearer, Header: http.Header{"Content-Type": {"application/json"}}, Body: raw})
}

// call invokes fn as the admin member, requires 200 and decodes the reply.
func call(t testing.TB, fx *fixture, fn string, body any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := invoke(t, fx.c, fn, fx.admin, body).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// rpcCode is the error envelope's code ({ok:false, error:{code,...}}).
func rpcCode(r *gw.Response) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if json.Unmarshal(r.Body, &env) != nil {
		return ""
	}
	if env.Error.Code != "" {
		return env.Error.Code
	}
	return env.Code
}

// sub decodes m[key] (a nested JSON object) into a map.
func sub(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}
