//go:build e2e_fleet

package deployments

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const (
	// deploySecretsDir holds each deployment's env and token files, root 0700,
	// files 0600 (core/pkg/deploysecrets).
	deploySecretsDir = "/var/lib/orama-deploy"
	// oramaData is where a node keeps its stores (the index rqlite under
	// rqlite/, namespaces under tenancy.NamespacesDir).
	oramaData = "/opt/orama/.orama/data"
	// maxValue is one environment value's cap (website/src/docs/developer/deployments.mdx "How
	// the values are handled").
	maxValue = 64 << 10
)

// marker is a random value that exists nowhere but where the test puts it.
func marker(t testing.TB) string {
	t.Helper()
	return "e2emark" + strings.ToLower(rand.Text())
}

func (tn *tenant) envOf(t testing.TB, u, key string) answer {
	t.Helper()
	return probe(t, tn.app(u), "/getenv", url.Values{"k": {key}})
}

// waitEnv waits until the running app sees key=want (present) or no key.
func (tn *tenant) waitEnv(t testing.TB, u, key, want string, present bool) {
	t.Helper()
	eventually.Require(t, pollEvery, startBudget, "the app to see "+key, func() (bool, error) {
		r, err := tn.app(u).Send(t.Context(), gw.Req{Path: "/getenv?k=" + url.QueryEscape(key)})
		if err != nil || r.Status != http.StatusOK {
			return false, fmt.Errorf("app not answering: %v", err)
		}
		var a answer
		decode(t, r, &a)
		if a.OK != present || (present && a.Detail != want) {
			return false, fmt.Errorf("sees ok=%v", a.OK)
		}
		return true, nil
	})
}

// TestDeployEnv_setAtDeployAndChangedLater: --env and --env-file reach the
// process, a --env overrides the file, `app env set|unset` restart it with the
// change, and `list` shows names only (website/src/docs/developer/deployments.mdx "Environment
// Variables").
func TestDeployEnv_setAtDeployAndChangedLater(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	secret := marker(t)
	envFile := filepath.Join(t.TempDir(), ".env")
	body := "# comment\nFROM_FILE=\"quoted value\"\nOVERRIDDEN=file\nDOLLAR=$HOME\n\n"
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "env"), "envapp", "--env-file", envFile,
		"--env", "OVERRIDDEN=flag", "--env", "SECRET="+secret, "--env", "HAS_EQ=a=b=c")
	serving(t, tn.app(u), "/health", "")
	for k, want := range map[string]string{"FROM_FILE": "quoted value", "OVERRIDDEN": "flag", "SECRET": secret, "HAS_EQ": "a=b=c", "DOLLAR": "$HOME"} {
		if a := tn.envOf(t, u, k); !a.OK || a.Detail != want {
			t.Errorf("%s: the app sees %q (set %v), want %q", k, a.Detail, a.OK, want)
		}
	}
	for _, k := range []string{"PORT", "ORAMA_NAMESPACE", "ORAMA_GATEWAY_URL", "ORAMA_STATE_DIR", "ORAMA_CACHE_DIR", "ORAMA_TOKEN_FILE"} {
		if a := tn.envOf(t, u, k); !a.OK || a.Detail == "" {
			t.Errorf("the platform did not set %s", k)
		}
	}
	if a := tn.envOf(t, u, "ORAMA_NAMESPACE"); a.Detail != tn.n.Name {
		t.Errorf("ORAMA_NAMESPACE is %q", a.Detail)
	}
	list := tn.cli.MustOK(t, "app", "env", "list", "envapp").Stdout
	if !strings.Contains(list, "SECRET") || strings.Contains(list, secret) {
		t.Fatalf("env list must show names and no value:\n%s", list)
	}
	tn.cli.MustOK(t, "app", "env", "set", "envapp", "--env", "LATER=added")
	tn.waitEnv(t, u, "LATER", "added", true)
	tn.cli.MustOK(t, "app", "env", "unset", "envapp", "LATER", "HAS_EQ")
	tn.waitEnv(t, u, "HAS_EQ", "", false)
	if r := tn.api(t, http.MethodGet, pathEnv+"?name=envapp", nil).Expect(t, http.StatusOK); strings.Contains(string(r.Body), secret) {
		t.Fatal("GET /v1/deployments/env returned a value")
	}
}

// TestDeployEnv_refusals: platform names, invalid UTF-8, NUL, an over-long
// value and an empty change are refused with 400, and the app keeps its
// environment (docs/whitepaper/technical-reference/vol1/11-app-deployments.md "Deployment environment variables").
func TestDeployEnv_refusals(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "envref"), "envref", "--env", "KEEP=yes")
	serving(t, tn.app(u), "/health", "")
	cases := map[string]any{
		"PORT":        map[string]any{"set": map[string]string{"PORT": "1"}},
		"ENTRY_POINT": map[string]any{"set": map[string]string{"ENTRY_POINT": "evil.js"}},
		"ORAMA_*":     map[string]any{"set": map[string]string{"ORAMA_GATEWAY_URL": "https://evil"}},
		"unset PORT":  map[string]any{"unset": []string{"PORT"}},
		"NUL":         map[string]any{"set": map[string]string{"A": "x\x00y"}},
		"over 64 KiB": map[string]any{"set": map[string]string{"BIG": strings.Repeat("v", maxValue+1)}},
		"nothing":     map[string]any{},
		"bad name":    map[string]any{"set": map[string]string{"A B": "x"}},
		"not json":    []byte("set=A"),
	}
	for name, body := range cases {
		if r := tn.api(t, http.MethodPost, pathEnvSet+"?name=envref", body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %.150s", name, r.Status, r.Body)
		}
	}
	// JSON cannot carry invalid UTF-8 (the decoder substitutes U+FFFD), so the
	// rule is exercised through a deploy upload's env_ form field.
	nodeTar := tarball(t, map[string]string{"package.json": plainPackage, "index.js": nodeServer})
	fields := map[string]string{"name": "badutf", "env_A": "\xff\xfe"}
	if r := tn.upload(t, "/v1/deployments/nodejs/upload", fields, "app.tar.gz", nodeTar); r.Status != http.StatusBadRequest {
		t.Errorf("an env value that is not UTF-8: want 400, got %d", r.Status)
		tn.deleteApp(t, "badutf")
	}
	tn.api(t, http.MethodPost, pathEnvSet+"?name=missing", map[string]any{"set": map[string]string{"A": "b"}}).Expect(t, http.StatusNotFound)
	tn.api(t, http.MethodPost, pathEnvSet+"?name=envref", map[string]any{"set": map[string]string{"AT_LIMIT": strings.Repeat("v", maxValue)}}).Expect(t, http.StatusOK)
	if res, err := tn.cli.Run(t.Context(), "deploy", "go", tenancy.WriteProbeApp(t, "x"), "--name", "badenv", "--env", "PORT=1"); err != nil || res.Exit == 0 {
		t.Errorf("a deploy setting PORT succeeded (%v)", err)
	}
	tn.waitEnv(t, u, "KEEP", "yes", true)
}

// TestDeployEnv_encryptedAtRest: a value is in no store on any node in the
// clear (docs/whitepaper/technical-reference/vol1/11-app-deployments.md: deployments.environment is AES-256-GCM encrypted);
// only the root-only 0600 file systemd hands the process holds it.
func TestDeployEnv_encryptedAtRest(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	secret := marker(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "rest"), "rest", "--env", "SECRET="+secret)
	serving(t, tn.app(u), "/health", "")
	// Only the stores that can hold this app's environment: the index (the
	// deployments table), this namespace's own stores and the app's directory.
	// Not every namespace's data: parallel tests' stores are none of its business.
	stores := strings.Join([]string{oramaData + "/rqlite", tenancy.NamespacesDir + "/index", tenancy.NamespacesDir + "/" + tn.n.Name,
		deploymentsDir + "/" + tn.instance("rest")}, " ")
	for _, node := range tn.f.State.Nodes {
		if out := tn.f.Exec(t, node, "grep -rl --binary-files=text "+secret+" "+stores+" 2>/dev/null | head -5"); strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s holds the value in the clear in %s", node.Name, strings.TrimSpace(out.Stdout))
		}
	}
	for _, node := range unitNodes(t, tn.f, "orama-deploy-go@"+tn.instance("rest")+".service") {
		out := tn.f.MustExec(t, node, "stat -c '%a %U' "+deploySecretsDir+" "+deploySecretsDir+"/orama-deploy-"+tn.instance("rest")+".env").Stdout
		if got := strings.Fields(out); len(got) != 4 || got[0] != "700" || got[1] != "root" || got[2] != "600" || got[3] != "root" {
			t.Errorf("%s: the env dir and file are %q, want 700 root and 600 root", node.Name, out)
		}
	}
}
