//go:build e2e_fleet

package referenceapps

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// sitePages are what a visitor of the marketing site loads.
var sitePages = []string{"/", "/pricing.html", "/docs/", "/assets/site.css", "/assets/site.js", "/robots.txt"}

// TestReferenceSite_marketingSiteVisitorsAndUpdate: the static marketing site
// deploys with `orama deploy static`, every node serves every page by name
// with its content type, 200 visitors' page views all succeed, and an update
// in place (`--update`) reaches every node (docs/DEPLOYMENT_GUIDE.md
// "Deploying Static Sites", "Updating a Deployment").
func TestReferenceSite_marketingSiteVisitorsAndUpdate(t *testing.T) {
	t.Parallel()
	tn := realistic.NewTenant(t)
	u := tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: "site-v1"}, nil), "site")
	tn.EveryNodeServes(t, u, "/", "site-v1")
	c := tn.App(u)
	for path, ctype := range map[string]string{"/pricing.html": "text/html", "/assets/site.css": "text/css", "/assets/site.js": "javascript", "/robots.txt": "text/plain"} {
		r := c.MustSend(t, gw.Req{Path: path}).Expect(t, http.StatusOK)
		if !strings.Contains(r.Header.Get("Content-Type"), ctype) {
			t.Errorf("%s served as %q, want %s", path, r.Header.Get("Content-Type"), ctype)
		}
	}
	visitors(t, tn.F, c, "site-visitors", 8, 200, sitePages)
	tn.Deploy(t, "static", realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: "site-v2"}, nil), "site", "--update")
	tn.EveryNodeServes(t, u, "/", "site-v2")
}

// renderID is the per-request id the Next.js page renders on the server.
var renderID = regexp.MustCompile(`<p id="render">([a-z0-9]+)</p>`)

// TestReferenceNext_ssrRendersPerRequestAndUpdates: a real Next.js project
// is installed, built and uploaded by `orama deploy nextjs --ssr`; every node
// serves it, each request is rendered on the server (two renders differ),
// its dependency directory was cleared by orama-deploy-clean@ because the
// standalone output ships its own, and `--update` (/v1/deployments/nextjs/update)
// replaces it on every node (docs/DEPLOYMENT_GUIDE.md "Next.js with SSR").
func TestReferenceNext_ssrRendersPerRequestAndUpdates(t *testing.T) {
	t.Parallel()
	requireNPM(t)
	tn := realistic.NewTenant(t)
	requireNodeRuntime(t, tn.F)
	src := func(marker string) string {
		return realistic.CopyApp(t, realistic.AppNextSSR, map[string]string{realistic.ReleaseMarker: marker}, nil)
	}
	u := tn.Deploy(t, "nextjs", src("next-v1"), "web", "--ssr", "--health-check", "/api/health")
	tn.EveryNodeServes(t, u, "/", "next-v1")
	c := tn.App(u)
	first, second := render(t, c), render(t, c)
	if first == second {
		t.Errorf("two requests got the same render %q: the page is not rendered per request", first)
	}
	requireReplicas(t, tn, "node", "web")
	requireCleanRan(t, tn, "web")
	visitors(t, tn.F, c, "next-visitors", 6, 120, []string{"/", "/api/health", "/robots.txt"})
	tn.Deploy(t, "nextjs", src("next-v2"), "web", "--ssr", "--update")
	tn.EveryNodeServes(t, u, "/", "next-v2")
}

func render(t testing.TB, c *gw.Client) string {
	t.Helper()
	r := c.MustSend(t, gw.Req{Path: "/"}).Expect(t, http.StatusOK)
	m := renderID.FindSubmatch(r.Body)
	if m == nil {
		t.Fatalf("the page has no server render id: %.300s", r.Body)
	}
	return string(m[1])
}

// requireCleanRan checks that orama-deploy-clean@ ran on a node that runs
// the app: an SSR upload ships node_modules, so the gateway clears whatever
// an earlier holder of the instance installed
// (core/pkg/gateway/handlers/deployments/nextjs_handler.go ClearDependencies).
// It is a oneshot, and systemd zeroes a finished oneshot's start timestamps,
// so whether it ran is read from its journal: a run that finished, and a last
// result of success.
func requireCleanRan(t testing.TB, tn *realistic.Tenant, name string) {
	t.Helper()
	unit := "orama-deploy-clean@" + tn.N.Name + "-" + name + ".service"
	for _, n := range appUnitNodes(t, tn, "node", name) {
		ran := tn.F.MustExec(t, n, "journalctl -u "+unit+" --no-pager -q -o cat | grep -c '^Finished ' || true").Stdout
		result := tn.F.MustExec(t, n, "systemctl show -p Result "+unit).Stdout
		if strings.TrimSpace(ran) == "0" || !strings.Contains(result, "Result=success") {
			t.Errorf("%s: %s did not run successfully for the SSR app (finished runs: %s; %s)",
				n.Name, unit, strings.TrimSpace(ran), strings.TrimSpace(result))
		}
	}
}
