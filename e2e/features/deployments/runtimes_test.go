//go:build e2e_fleet

package deployments

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// everyNodeServes asks each core node, by the app's own name, for path: DNS is
// round-robin and a node without the app proxies to its home node
// (docs/DEPLOYMENT_GUIDE.md "Cross-Node Routing").
func everyNodeServes(t testing.TB, tn *tenant, appURL, path, want string) {
	t.Helper()
	for _, nc := range tenancy.PerNode(t, tn.f, tn.app(appURL)) {
		serving(t, nc.Client, path, want)
	}
}

// TestDeployStatic_servesSPAFromEveryNode: a static site is served with the
// right content types and cache header, unknown routes fall back to
// index.html (docs/DEPLOYMENT_GUIDE.md "Deploying Static Sites").
func TestDeployStatic_servesSPAFromEveryNode(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "static", staticSite(t, "static-v1"), "site")
	everyNodeServes(t, tn, u, "/", "static-v1")
	c := tn.app(u)
	for path, ctype := range map[string]string{"/assets/app.css": "text/css", "/data.json": "application/json", "/": "text/html"} {
		r := c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK)
		if !strings.HasPrefix(r.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s served as %q, want %s", path, r.Header.Get("Content-Type"), ctype)
		}
		if cc := r.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Errorf("%s has Cache-Control %q", path, cc)
		}
	}
	if r := c.MustSend(t, gw.Req{Path: "/deep/client/route"}); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "static-v1") {
		t.Errorf("an unknown route did not fall back to index.html: %d", r.Status)
	}
	for _, hostile := range []string{"/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd", "/assets/..%2f..%2findex.html"} {
		if r := c.MustSend(t, gw.Req{Path: hostile}); strings.Contains(string(r.Body), "root:") {
			t.Fatalf("%s escaped the site: %.100s", hostile, r.Body)
		}
	}
}

// TestDeployNextStatic_exportServed: a Next.js static export deploys as a
// static site (docs/DEPLOYMENT_GUIDE.md "Static Next.js Export").
func TestDeployNextStatic_exportServed(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "static", nextStaticExport(t, "next-export"), "nextout")
	everyNodeServes(t, tn, u, "/", "next-export")
	serving(t, tn.app(u), "/_next/static/chunks/main.js", "next-export")
}

// TestDeployNextSSR_standaloneServerRuns: an SSR upload is a standalone
// server.js the platform runs with node (docs/DEPLOYMENT_GUIDE.md "What Happens
// Behind the Scenes"). The tarball is sent over the API: building one with
// `orama deploy nextjs --ssr` needs npm and Next.js on the runner (the next
// test does that when npm is present).
func TestDeployNextSSR_standaloneServerRuns(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tenancy.RequireNodeRuntime(t, tn.f)
	tgz := tarball(t, map[string]string{
		"server.js":           replaceVersion(nodeServer, "next-ssr"),
		"package.json":        `{"name":"ssr","version":"1.0.0","main":"server.js"}`,
		".next/static/app.js": "console.log('ssr')",
		"public/robots.txt":   "User-agent: *",
	})
	r := tn.upload(t, "/v1/deployments/nextjs/upload", map[string]string{"name": "ssr", "ssr": "true"}, "ssr.tar.gz", tgz)
	r.Expect(t, http.StatusCreated)
	t.Cleanup(func() { tn.deleteApp(t, "ssr") })
	var out struct {
		URLs []string `json:"urls"`
	}
	decode(t, r, &out)
	if len(out.URLs) == 0 {
		t.Fatalf("the SSR upload returned no URL: %s", r.Body)
	}
	everyNodeServes(t, tn, out.URLs[0], "/version", "next-ssr")
	if nodes := unitNodes(t, tn.f, "orama-deploy-node@"+tn.instance("ssr")+".service"); len(nodes) == 0 {
		t.Error("no node runs the SSR app under orama-deploy-node@")
	}
}

// TestDeployNextSSR_cliBuildsAndDeploys drives `orama deploy nextjs --ssr` on
// a minimal real Next.js project: the CLI installs, builds and uploads the
// standalone output.
func TestDeployNextSSR_cliBuildsAndDeploys(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("npm"); err != nil {
		harness.SkipNotApplicable(t, "the runner has no npm; `orama deploy nextjs --ssr` builds the app on the operator's machine")
	}
	tn := newTenant(t)
	tenancy.RequireNodeRuntime(t, tn.f)
	dir := writeTree(t, map[string]string{
		"package.json":        `{"name":"e2e-next","private":true,"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"14.2.15","react":"18.3.1","react-dom":"18.3.1"}}`,
		"next.config.js":      "module.exports = { output: 'standalone' }\n",
		"pages/index.js":      "export default function Home() { return <p>next-cli-ssr</p> }\nexport async function getServerSideProps() { return { props: {} } }\n",
		"pages/api/health.js": "export default function handler(req, res) { res.status(200).json({ ok: true }) }\n",
	})
	u := tn.deploy(t, "nextjs", dir, "nextcli", "--ssr", "--health-check", "/api/health")
	everyNodeServes(t, tn, u, "/", "next-cli-ssr")
}

// TestDeployNode_cliDeployServes: a Node.js backend with no dependencies is
// installed on the server and run with node <main>.
func TestDeployNode_cliDeployServes(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tenancy.RequireNodeRuntime(t, tn.f)
	u := tn.deploy(t, "nodejs", nodeApp(t, "node-v1", plainPackage), "nodeapp")
	everyNodeServes(t, tn, u, "/version", "node-v1")
	if nodes := unitNodes(t, tn.f, "orama-deploy-node@"+tn.instance("nodeapp")+".service"); len(nodes) == 0 {
		t.Error("no node runs the app under orama-deploy-node@")
	}
}

// TestDeployNode_startScriptRunsUnderNPM: a package.json with a start script
// runs `npm start` under orama-deploy-npm@ (docs/DEPLOYMENT_GUIDE.md "Start
// Command Detection"); its install ran in orama-deploy-build@.
func TestDeployNode_startScriptRunsUnderNPM(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	tenancy.RequireNodeRuntime(t, tn.f, tenancy.NodeBinary, tenancy.NPMBinary)
	// A start script that is exactly "node <file>" runs under node@ directly
	// (nodejs_handler.go detectEntryPoint); only anything else goes to npm.
	pkg := `{"name":"e2e-npm","version":"1.0.0","scripts":{"start":"NODE_ENV=production node index.js"}}`
	u := tn.deploy(t, "nodejs", nodeApp(t, "npm-start", pkg), "npmapp")
	everyNodeServes(t, tn, u, "/version", "npm-start")
	if nodes := unitNodes(t, tn.f, "orama-deploy-npm@"+tn.instance("npmapp")+".service"); len(nodes) == 0 {
		t.Error("no node runs the app under orama-deploy-npm@")
	}
	for _, node := range tn.f.State.Nodes {
		if buildInstallRan(tn.f.Exec(t, node, "systemctl show -p Result -p ExecMainExitTimestamp orama-deploy-build@"+tn.instance("npmapp")+".service").Stdout) {
			return
		}
	}
	t.Error("no node shows a successful orama-deploy-build@ install for the app")
}

// buildInstallRan reads `systemctl show -p Result -p ExecMainExitTimestamp` of
// a build unit: Result is "success" for a unit that never ran too, so the
// install counts only when it also recorded an exit time.
func buildInstallRan(show string) bool {
	var result, exited string
	for _, line := range strings.Split(show, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Result="); ok {
			result = v
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecMainExitTimestamp="); ok {
			exited = v
		}
	}
	return result == "success" && exited != ""
}

// TestDeployGo_cliCrossCompilesAndServes: `orama deploy go` builds
// linux/amd64 on the runner and the node runs it under orama-deploy-go@.
func TestDeployGo_cliCrossCompilesAndServes(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "go-v1"), "goapp")
	everyNodeServes(t, tn, u, "/version", "go-v1")
	nodes := unitNodes(t, tn.f, "orama-deploy-go@"+tn.instance("goapp")+".service")
	if len(nodes) != replicas {
		t.Errorf("%d nodes run the app, want %d (DefaultReplicaCount, core/pkg/deployments/types.go)", len(nodes), replicas)
	}
}

// replicas is how many nodes run a dynamic deployment: its home node and
// one replica (core/pkg/deployments/types.go DefaultReplicaCount).
const replicas = 2
