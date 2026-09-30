//go:build e2e_fleet

package deployments

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// deployTypeGo is what the gateway calls a Go backend deployment
// (core/pkg/deployments/types.go); `orama deploy go` is the CLI's runtime name.
const deployTypeGo = "go-backend"

// TestDeployLifecycle_updateRollbackVersions: an update bumps the version and
// serves the new build; versions lists both; rollback serves the old build
// again; list, get, logs, stats and events describe the app; delete removes it
// everywhere (docs/CLI_REFERENCE.md "orama app", "orama deploy").
func TestDeployLifecycle_updateRollbackVersions(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "go", tenancy.WriteProbeApp(t, "life-v1"), "life")
	serving(t, tn.app(u), "/version", "life-v1")
	tn.deploy(t, "go", tenancy.WriteProbeApp(t, "life-v2"), "life", "--update")
	serving(t, tn.app(u), "/version", "life-v2")
	var versions struct {
		Current  int `json:"current_version"`
		Versions []struct {
			Version int `json:"version"`
		} `json:"versions"`
	}
	decode(t, tn.api(t, http.MethodGet, pathVersions+"?name=life", nil).Expect(t, http.StatusOK), &versions)
	seen := map[int]int{}
	for _, v := range versions.Versions {
		seen[v.Version]++
	}
	if versions.Current != 2 || seen[1] != 1 || seen[2] != 1 || len(seen) != 2 {
		t.Errorf("after one update the versions are %+v at current %d, want exactly one row each for 1 and 2", versions.Versions, versions.Current)
	}
	// The rollback asks "(y/N)" on stdin; without an answer it cancels and
	// exits 0, so every run here answers y and a refusal is the API's.
	confirm := oramacli.RunOpts{Stdin: []byte("y\n")}
	rolled, err := tn.cli.RunWith(t.Context(), confirm, "app", "rollback", "life", "--version", "1")
	if err != nil || rolled.Exit != 0 || strings.Contains(rolled.Stdout, "Cancelled") {
		t.Fatalf("orama app rollback life --version 1: exit %d %v %s", rolled.Exit, err, rolled.Stdout)
	}
	serving(t, tn.app(u), "/version", "life-v1")
	for _, bad := range [][]string{{"app", "rollback", "life", "--version", "99"}, {"app", "rollback", "life"}, {"app", "rollback", "nope", "--version", "1"}} {
		if res, err := tn.cli.RunWith(t.Context(), confirm, bad...); err != nil || res.Exit == 0 {
			t.Errorf("orama %s succeeded (%v): %s", strings.Join(bad, " "), err, res.Stdout)
		}
	}
	tn.describes(t, "life")
	tn.api(t, http.MethodDelete, pathDelete+"?name=life", nil).Expect(t, http.StatusOK)
	tn.api(t, http.MethodGet, pathGet+"?name=life", nil).Expect(t, http.StatusNotFound)
	unit := "orama-deploy-go@" + tn.instance("life") + ".service"
	eventually.Require(t, pollEvery, startBudget, "every node to stop the deleted app", func() (bool, error) {
		return len(unitNodes(t, tn.f, unit)) == 0, nil
	})
}

// describes checks list, get, logs, stats and events for a running Go app.
func (tn *tenant) describes(t testing.TB, name string) {
	t.Helper()
	var list struct {
		Deployments []struct{ Name, Type, Status string } `json:"deployments"`
	}
	if err := oramacli.DecodeJSON(tn.cli.MustOK(t, "app", "list", "--json"), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Deployments) == 0 || list.Deployments[0].Name != name || list.Deployments[0].Type != deployTypeGo {
		t.Errorf("app list shows %+v", list.Deployments)
	}
	if out := tn.cli.MustOK(t, "app", "get", name).Stdout; !regexp.MustCompile(`(?m)^Type:\s+`+deployTypeGo+`$`).MatchString(out) || !strings.Contains(out, "https://") {
		t.Errorf("app get printed %q", out)
	}
	tn.cli.MustOK(t, "app", "logs", name, "-n", "20")
	tn.cli.MustOK(t, "app", "stats", name)
	for _, p := range []string{pathLogs, pathStats, pathEvents} {
		tn.api(t, http.MethodGet, p+"?name="+url.QueryEscape(name), nil).Expect(t, http.StatusOK)
		tn.api(t, http.MethodGet, p, nil).Expect(t, http.StatusBadRequest)
		if r := tn.api(t, http.MethodGet, p+"?name=missing", nil); r.Status != http.StatusNotFound {
			t.Errorf("%s of a missing app answered %d", p, r.Status)
		}
	}
	tn.api(t, http.MethodGet, pathList, nil).Expect(t, http.StatusOK)
}

// TestDeployLifecycle_cliDeleteNeedsConfirmation: `orama app delete` asks for
// "y"; with nothing typed nothing is deleted.
func TestDeployLifecycle_cliDeleteNeedsConfirmation(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "static", staticSite(t, "keep-me"), "keep")
	out := tn.cli.MustOK(t, "app", "delete", "keep").Stdout
	if !strings.Contains(out, "Cancelled") {
		t.Fatalf("an unconfirmed delete printed %q", out)
	}
	serving(t, tn.app(u), "/", "keep-me")
	help := tn.cli.MustOK(t, "app", "--help").Stdout
	for _, sub := range []string{"delete", "env", "get", "grants", "list", "logs", "rollback", "stats"} {
		if !strings.Contains(help, sub) {
			t.Errorf("orama app --help does not list %s", sub)
		}
	}
	if help := tn.cli.MustOK(t, "deploy", "--help").Stdout; !strings.Contains(help, "nodejs") || !strings.Contains(help, "static") {
		t.Errorf("orama deploy --help is %q", help)
	}
}

// TestDeployLifecycle_staticUpdateAndRollback: static sites version too.
func TestDeployLifecycle_staticUpdateAndRollback(t *testing.T) {
	t.Parallel()
	tn := newTenant(t)
	u := tn.deploy(t, "static", staticSite(t, "s-v1"), "sv")
	serving(t, tn.app(u), "/", "s-v1")
	tn.deploy(t, "static", staticSite(t, "s-v2"), "sv", "--update")
	serving(t, tn.app(u), "/", "s-v2")
	tn.api(t, http.MethodPost, pathRollback, map[string]any{"name": "sv", "version": 1}).Expect(t, http.StatusOK)
	serving(t, tn.app(u), "/", "s-v1")
	for name, body := range map[string]any{"no name": map[string]any{"version": 1}, "zero": map[string]any{"name": "sv", "version": 0}} {
		if r := tn.api(t, http.MethodPost, pathRollback, body); r.Status != http.StatusBadRequest {
			t.Errorf("rollback %s: want 400, got %d", name, r.Status)
		}
	}
	if r := tn.api(t, http.MethodGet, pathLogs+"?name=sv", nil); r.Status != http.StatusBadRequest {
		t.Errorf("logs of a static site answered %d, want 400", r.Status)
	}
}
