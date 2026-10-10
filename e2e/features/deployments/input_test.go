//go:build e2e_fleet

package deployments

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

const staticUpload = "/v1/deployments/static/upload"

// TestDeployInput_namesValidated: 1-56 of letters, digits, - and _, starting
// with a letter or digit; anything else is 400 before the upload is stored
// (website/src/docs/developer/deployments.mdx "Deployment names").
func TestDeployInput_namesValidated(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	site := tarball(t, map[string]string{"index.html": "x"})
	for _, name := range []string{"", "-lead", "_lead", "a.b", "a/b", "../up", "a b", "ünï", strings.Repeat("n", 57), "a\x00b", "a;rm"} {
		if r := tn.upload(t, staticUpload, map[string]string{"name": name}, "s.tar.gz", site); r.Status != http.StatusBadRequest {
			t.Errorf("name %q: want 400, got %d", name, r.Status)
			tn.deleteApp(t, name)
		}
	}
	for _, name := range []string{strings.Repeat("n", 56), "A_1-b"} {
		tn.upload(t, staticUpload, map[string]string{"name": name}, "s.tar.gz", site).Expect(t, http.StatusCreated)
		t.Cleanup(func() { tn.deleteApp(t, name) })
	}
	if r := tn.upload(t, staticUpload, map[string]string{"name": "A_1-b"}, "s.tar.gz", site); r.Status != http.StatusConflict {
		t.Errorf("a second deployment with the same name: want 409, got %d", r.Status)
	}
}

// TestDeployInput_badArchivesRefused: no archive, the wrong extension, an
// empty file, a file that is not gzip, and a tar with a traversal entry are
// refused, and nothing is recorded.
func TestDeployInput_badArchivesRefused(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	cases := map[string]struct {
		filename string
		body     []byte
	}{
		"no archive":        {"", nil},
		"wrong extension":   {"site.zip", tarball(t, map[string]string{"index.html": "x"})},
		"empty":             {"site.tar.gz", []byte{}},
		"not gzip":          {"site.tar.gz", []byte("this is not an archive")},
		"traversal entry":   {"site.tar.gz", tarball(t, map[string]string{"../../escape.html": "x"})},
		"truncated archive": {"site.tar.gz", tarball(t, map[string]string{"index.html": strings.Repeat("x", 4096)})[:40]},
	}
	i := 0
	for what, c := range cases {
		i++
		name := "bad" + strconv.Itoa(i)
		if r := tn.upload(t, staticUpload, map[string]string{"name": name}, c.filename, c.body); r.Status < http.StatusBadRequest {
			t.Errorf("%s: the upload was accepted (%d)", what, r.Status)
			tn.deleteApp(t, name)
			continue
		}
		if tn.api(t, http.MethodGet, pathGet+"?name="+name, nil).Status == http.StatusOK {
			t.Errorf("%s: a refused upload left a deployment", what)
			tn.deleteApp(t, name)
		}
	}
	for _, node := range tn.f.State.Nodes {
		if tn.f.Exec(t, node, "test -e /escape.html -o -e /etc/cron.d/x -o -e "+deploymentsDir+"/escape.html").Exit == 0 {
			t.Errorf("%s: an archive entry escaped its directory", node.Name)
		}
	}
}

// TestDeployInput_absoluteEntryStaysInsideTheSite: a tar entry named
// /etc/cron.d/x is joined under the site's own directory
// (static_handler.go extractTarball), so the upload is accepted and the file
// is served from inside the site; nothing lands at the absolute path on any
// node.
func TestDeployInput_absoluteEntryStaysInsideTheSite(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	r := tn.upload(t, staticUpload, map[string]string{"name": "absolute"},
		"site.tgz", tarball(t, map[string]string{"index.html": "abs-index", "/etc/cron.d/x": "abs-inside"}))
	r.Expect(t, http.StatusCreated)
	t.Cleanup(func() { tn.deleteApp(t, "absolute") })
	var out struct {
		URLs []string `json:"urls"`
	}
	decode(t, r, &out)
	if len(out.URLs) == 0 {
		t.Fatalf("the upload returned no URL: %s", r.Body)
	}
	serving(t, tn.app(out.URLs[0]), "/etc/cron.d/x", "abs-inside")
	for _, node := range tn.f.State.Nodes {
		if tn.f.Exec(t, node, "test -e /etc/cron.d/x").Exit == 0 {
			t.Errorf("%s: an absolute archive entry was written outside its site", node.Name)
		}
	}
}

// TestDeployInput_concurrentSameNameOneWinner: parallel creations of one name
// leave exactly one deployment; the others are refused 409.
func TestDeployInput_concurrentSameNameOneWinner(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	const racers = 5
	site := tarball(t, map[string]string{"index.html": "race"})
	statuses := make([]int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses[i] = tn.uploadNoFail(t, map[string]string{"name": "race"}, site)
		}()
	}
	wg.Wait()
	t.Cleanup(func() { tn.deleteApp(t, "race") })
	won := 0
	for i, st := range statuses {
		switch st {
		case http.StatusCreated:
			won++
		case http.StatusConflict:
		default:
			t.Errorf("racer %d: want 201 or 409, got %d", i, st)
		}
	}
	if won != 1 {
		t.Fatalf("%d racers created the deployment, want exactly 1 (%v)", won, statuses)
	}
}

// uploadNoFail is a static upload from a goroutine: it reports the status
// (0 when the request could not be made) instead of failing the test.
func (tn *tenant) uploadNoFail(t testing.TB, fields map[string]string, tgz []byte) int {
	req, err := multipartReq(fields, "s.tar.gz", tgz)
	if err != nil {
		return 0
	}
	req.Path, req.Bearer = staticUpload, tn.admin.Bearer
	r, err := tn.n.Client.Send(t.Context(), req)
	if err != nil {
		return 0
	}
	return r.Status
}

// TestDeployAuth_controlPlaneOnly: deploying is the admin grant's; no
// credential and a runtime member are refused, and nothing is deployed.
func TestDeployAuth_controlPlaneOnly(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	runtime := tenancy.OperatorMember(t, tn.f, tn.n, tenancy.RoleRuntime)
	site := tarball(t, map[string]string{"index.html": "x"})
	for who, cred := range map[string]tenancy.Cred{"nobody": {}, "runtime member": {Bearer: runtime.Token()}} {
		req, err := multipartReq(map[string]string{"name": "denied"}, "s.tar.gz", site)
		if err != nil {
			t.Fatal(err)
		}
		req.Path, req.Bearer = staticUpload, cred.Bearer
		if r := tn.n.Client.MustSend(t, req); r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
			t.Errorf("%s deployed: %d", who, r.Status)
		}
		for _, p := range []string{pathList, pathEnv + "?name=x", pathGrants} {
			if r := tenancy.Get(t, tn.n.Client, p, cred); r.Status != http.StatusUnauthorized && r.Status != http.StatusForbidden {
				t.Errorf("%s read %s: %d", who, p, r.Status)
			}
		}
	}
	tn.api(t, http.MethodGet, pathGet+"?name=denied", nil).Expect(t, http.StatusNotFound)
}

// TestDeployRuntime_crashIsRestarted: an app that exits is restarted by its
// unit and serves again.
func TestDeployRuntime_crashIsRestarted(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "crash"), "crasher")
	serving(t, tn.app(u), "/health", "")
	unit := "orama-deploy-go@" + tn.instance("crasher") + ".service"
	before := restarts(t, tn, unit)
	for range 3 {
		tn.app(u).MustSend(t, gw.Req{Path: "/crash"})
		serving(t, tn.app(u), "/health", "")
	}
	eventually.Require(t, pollEvery, startBudget, "the unit to record the restarts", func() (bool, error) {
		if now := restarts(t, tn, unit); now <= before {
			return false, fmt.Errorf("NRestarts %d, was %d", now, before)
		}
		return true, nil
	})
}

// restarts sums NRestarts of unit over the nodes that run it.
func restarts(t testing.TB, tn *tenant, unit string) int {
	t.Helper()
	total := 0
	for _, node := range tn.f.State.Nodes {
		out := strings.TrimSpace(tn.f.Exec(t, node, "systemctl show -p NRestarts --value "+unit).Stdout)
		if n, err := strconv.Atoi(out); err == nil {
			total += n
		}
	}
	return total
}
