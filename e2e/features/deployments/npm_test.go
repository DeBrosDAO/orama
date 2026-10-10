//go:build e2e_fleet

package deployments

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
)

// nodeUpload is the Node.js upload route the CLI uses.
const nodeUpload = "/v1/deployments/nodejs/upload"

// pwnScript writes a marker wherever an install script would run.
const pwnScript = `node -e \"require('fs').writeFileSync('node_modules/.pwned','1');require('fs').writeFileSync('pwned','1')\"`

// TestDeployNPM_installScriptsNeverRun: the server installs with
// --ignore-scripts, so a package's own preinstall/install/postinstall/prepare
// scripts do not run (website/src/docs/developer/deployments.mdx "How the server installs them").
func TestDeployNPM_installScriptsNeverRun(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tenancy.RequireNodeRuntime(t, tn.f, tenancy.NodeBinary, tenancy.NPMBinary)
	pkg := `{"name":"hostile","version":"1.0.0","main":"index.js","scripts":{` +
		`"preinstall":"` + pwnScript + `","install":"` + pwnScript + `","postinstall":"` + pwnScript + `","prepare":"` + pwnScript + `"},` +
		`"dependencies":{"ms":"2.1.3"}}`
	u := tn.deploy(t, "nodejs", nodeApp(t, "npm", pkg), "hostile")
	serving(t, tn.app(u), "/health", "")
	if a := probe(t, tn.app(u), "/marker", nil); !a.OK {
		t.Fatalf("an install script ran on the server: %s", a.Detail)
	}
	for _, node := range tn.f.State.Nodes {
		if out := tn.f.Exec(t, node, "find /var/cache/orama-build/"+tn.instance("hostile")+" "+deploymentsDir+"/"+tn.instance("hostile")+" -name '.pwned' -o -name pwned 2>/dev/null"); strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s: an install script left %s", node.Name, strings.TrimSpace(out.Stdout))
		}
	}
}

// TestDeployNPM_nonRegistrySourcesRefused: a git, URL, file, link or path
// dependency — in any dependency field, overrides or the lockfile — and
// workspaces are refused before the install starts (website/src/docs/developer/deployments.mdx).
func TestDeployNPM_nonRegistrySourcesRefused(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	pkgs := map[string]string{
		"github dep":   `{"name":"a","version":"1.0.0","dependencies":{"x":"github:evil/x"}}`,
		"user/repo":    `{"name":"a","version":"1.0.0","dependencies":{"x":"evil/x"}}`,
		"git+ssh":      `{"name":"a","version":"1.0.0","dependencies":{"x":"git+ssh://git@evil.example/x.git"}}`,
		"url tarball":  `{"name":"a","version":"1.0.0","dependencies":{"x":"https://evil.example/x.tgz"}}`,
		"file:":        `{"name":"a","version":"1.0.0","dependencies":{"x":"file:../x"}}`,
		"link:":        `{"name":"a","version":"1.0.0","dependencies":{"x":"link:../x"}}`,
		"path":         `{"name":"a","version":"1.0.0","dependencies":{"x":"../x"}}`,
		"optional git": `{"name":"a","version":"1.0.0","optionalDependencies":{"x":"github:evil/x"}}`,
		"peer url":     `{"name":"a","version":"1.0.0","peerDependencies":{"x":"https://evil.example/x.tgz"}}`,
		"overrides":    `{"name":"a","version":"1.0.0","dependencies":{"ms":"2.1.3"},"overrides":{"ms":"github:evil/ms"}}`,
		"workspaces":   `{"name":"a","version":"1.0.0","workspaces":["packages/*"]}`,
	}
	for name, pkg := range pkgs {
		tn.expectInstallRefused(t, name, refusalFor(name), map[string]string{"package.json": pkg, "index.js": nodeServer})
	}
	lock := `{"name":"a","version":"1.0.0","lockfileVersion":3,"packages":{"":{"dependencies":{"ms":"2.1.3"}},` +
		`"node_modules/ms":{"version":"2.1.3","resolved":"http://evil.example/ms-2.1.3.tgz"}}}`
	tn.expectInstallRefused(t, "plain-http lockfile entry", refusalRegistryTarball, map[string]string{
		"package.json": `{"name":"a","version":"1.0.0","dependencies":{"ms":"2.1.3"}}`, "package-lock.json": lock, "index.js": nodeServer})
	gitLock := strings.Replace(lock, "http://evil.example/ms-2.1.3.tgz", "git+https://evil.example/ms.git", 1)
	tn.expectInstallRefused(t, "git lockfile entry", refusalRegistryTarball, map[string]string{
		"package.json": `{"name":"a","version":"1.0.0","dependencies":{"ms":"2.1.3"}}`, "package-lock.json": gitLock, "index.js": nodeServer})
}

// What the server's refusal says (core/pkg/deployments/process/npmspec.go).
const (
	refusalNotRegistry     = "is not a registry version"
	refusalWorkspaces      = "workspaces are not supported"
	refusalRegistryTarball = "not a registry tarball"
)

// refusalFor is the message expected of the manifest case named what.
func refusalFor(what string) string {
	switch what {
	case "workspaces":
		return refusalWorkspaces
	default:
		return refusalNotRegistry
	}
}

// expectInstallRefused uploads files as a Node.js app and requires the
// refusal (500 with a message naming the problem, per the guide) with no app
// left running. A bare "status >= 400" would also pass when npm is missing or a
// registry host is unreachable, with the guard deleted.
func (tn *tenant) expectInstallRefused(t testing.TB, what, message string, files map[string]string) {
	t.Helper()
	name := "npm" + strings.NewReplacer(" ", "", ":", "", "/", "", "+", "", "-", "").Replace(what)
	if len(name) > 40 {
		name = name[:40]
	}
	r := tn.upload(t, nodeUpload, map[string]string{"name": name}, "app.tar.gz", tarball(t, files))
	if r.Status < http.StatusBadRequest {
		tn.deleteApp(t, name)
		t.Errorf("%s: the install was accepted (%d)", what, r.Status)
		return
	}
	if r.Status != http.StatusInternalServerError || !strings.Contains(string(r.Body), message) {
		t.Errorf("%s: want HTTP 500 saying %q, got %d: %.300s", what, message, r.Status, r.Body)
	}
	if tn.api(t, http.MethodGet, pathGet+"?name="+name, nil).Status == http.StatusOK {
		tn.deleteApp(t, name)
		t.Errorf("%s: a refused install left a deployment record", what)
	}
}
