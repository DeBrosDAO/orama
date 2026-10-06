package legacylayout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyUnit is a unit as 0.122.x's process manager rendered it.
const legacyUnit = `[Unit]
Description=Orama Deployment - acme/web
After=network.target

[Service]
Type=simple
WorkingDirectory=/opt/orama/.orama/deployments/acme/web

Environment="PORT=10500"
Environment="DATABASE_URL=postgres://u:p@h/db?x=1"
Environment="ENTRY_POINT=../../../etc/evil.js"
Environment="ORAMA_NAMESPACE=victim"
Environment="ORAMA_GATEWAY_URL=https://attacker.example"

ExecStart=/usr/bin/node server.js
`

// 0.122.x nested deployments by namespace and name; the templates run them from
// data/deployments/<instance>. The move flattens each one, writes the owner
// marker the gateways' host-level claim reads, and stages the environment its
// legacy unit ran with.
func TestRun_flattensDeploymentsAndMarksTheirOwner(t *testing.T) {
	f := newFixture(t)
	f.write(t, "deployments/acme/web/server.js", "js")
	f.write(t, "deployments/my.org/api.v2/app", "bin")
	writeFile(t, filepath.Join(f.systemdDir, "orama-deploy-acme-web.service"), legacyUnit)

	if err := f.run(t); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for rel, want := range map[string]string{
		"data/deployments/acme-web/server.js": "js",
		"data/deployments/my-org-api-v2/app":  "bin",
	} {
		if got := mustRead(t, f.path(rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	for dir, want := range map[string]ownerMarker{
		"data/deployments/acme-web":      {Namespace: "acme", Name: "web"},
		"data/deployments/my-org-api-v2": {Namespace: "my.org", Name: "api.v2"},
	} {
		var got ownerMarker
		if err := json.Unmarshal([]byte(mustRead(t, f.path(dir+"/"+ownerMarkerName))), &got); err != nil || got != want {
			t.Errorf("%s owner = %+v (%v), want %+v", dir, got, err, want)
		}
	}
	assertGone(t, f.path("deployments"))

	env := f.stager.envs["acme-web"]
	if !strings.Contains(env, `DATABASE_URL="postgres://u:p@h/db?x=1"`) {
		t.Errorf("staged env %q lacks the tenant's variables", env)
	}
	// The platform's variables and the legacy entry point are the gateway's to
	// set; 0.122.x wrote them from a tenant-writable row.
	for _, key := range []string{"PORT=", "ENTRY_POINT=", "ORAMA_NAMESPACE=", "ORAMA_GATEWAY_URL="} {
		if strings.Contains(env, key) {
			t.Errorf("staged env carries the platform key %s: %q", key, env)
		}
	}
	if _, staged := f.stager.envs["my-org-api-v2"]; staged {
		t.Error("an environment was staged for a deployment with no legacy unit")
	}
}

// An archive extracted by 0.122.x may carry an owner marker of its own; only
// the migration knows the owner, so it replaces it.
func TestRun_replacesAnOwnerMarkerTheArchiveCarried(t *testing.T) {
	f := newFixture(t)
	f.write(t, "deployments/acme/web/"+ownerMarkerName, `{"namespace":"victim","name":"db"}`)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	var got ownerMarker
	if err := json.Unmarshal([]byte(mustRead(t, f.path("data/deployments/acme-web/"+ownerMarkerName))), &got); err != nil {
		t.Fatal(err)
	}
	if got != (ownerMarker{Namespace: "acme", Name: "web"}) {
		t.Errorf("owner = %+v", got)
	}
}

// Anything the old layout did not write is refused before anything moves: a
// file where a namespace or deployment directory belongs, a symlink, two
// deployments that map to one instance.
func TestRun_refusesWhatTheOldLayoutDidNotWrite(t *testing.T) {
	for name, seed := range map[string]func(t *testing.T, f *fixture){
		"file at namespace level":  func(t *testing.T, f *fixture) { f.write(t, "deployments/stray.tar.gz", "x") },
		"file at deployment level": func(t *testing.T, f *fixture) { f.write(t, "deployments/acme/stray", "x") },
		"symlinked deployment": func(t *testing.T, f *fixture) {
			if err := os.MkdirAll(f.path("deployments/acme"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), f.path("deployments/acme/web")); err != nil {
				t.Fatal(err)
			}
		},
		"two deployments, one instance": func(t *testing.T, f *fixture) {
			f.write(t, "deployments/a/b-c/app", "1")
			f.write(t, "deployments/a-b/c/app", "2")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.write(t, "deployments/good/app/server.js", "js")
			f.write(t, "sqlite/acme/app.db", "db")
			seed(t, f)
			if err := f.run(t); err == nil {
				t.Fatal("expected a refusal")
			}
			if _, err := os.Stat(f.path("deployments/good/app/server.js")); err != nil {
				t.Errorf("a refused migration moved a deployment: %v", err)
			}
			if _, err := os.Stat(f.path("sqlite/acme/app.db")); err != nil {
				t.Errorf("a refused migration moved another tree: %v", err)
			}
		})
	}
}

// A helper that refuses the environment leaves the deployment where it was,
// so the next start stages it and moves it then.
func TestRun_deploymentStaysUntilItsEnvironmentIsStaged(t *testing.T) {
	f := newFixture(t)
	f.write(t, "deployments/acme/web/server.js", "js")
	writeFile(t, filepath.Join(f.systemdDir, "orama-deploy-acme-web.service"), legacyUnit)
	f.stager.err = errHelperDown
	if err := f.run(t); err == nil {
		t.Fatal("a refused staging was reported as success")
	}
	if _, err := os.Stat(f.path("deployments/acme/web/server.js")); err != nil {
		t.Fatalf("the deployment moved before its environment was staged: %v", err)
	}
	f.stager.err = nil
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	if f.stager.envs["acme-web"] == "" {
		t.Error("the retry did not stage the environment")
	}
}

func TestLegacyDeploymentUnit(t *testing.T) {
	for name, want := range map[string]string{
		"orama-deploy-acme-web.service":      "acme-web",
		"orama-deploy-my-org-api-v2.service": "my-org-api-v2",
	} {
		if got, ok := LegacyDeploymentUnit(name); !ok || got != want {
			t.Errorf("%s = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{
		"orama-deploy-node@.service", "orama-deploy-node@acme-web.service", "orama-deploy-build@x.service",
		"orama-node.service", "orama-deploy-.service", "orama-deploy-../x.service", "orama-deploy-a b.service",
		"orama-deploy-acme-web.service.d", "orama-deploy--x.service",
	} {
		if got, ok := LegacyDeploymentUnit(name); ok {
			t.Errorf("%s was read as legacy unit %q", name, got)
		}
	}
}

func TestParseUnitEnvironment_refusesWhatItCannotRead(t *testing.T) {
	for _, unit := range []string{"Environment=PORT=1\n", `Environment="=1"` + "\n", `Environment="NOEQUALS"` + "\n"} {
		if _, err := parseUnitEnvironment(unit); err == nil {
			t.Errorf("%q was parsed", unit)
		}
	}
}
