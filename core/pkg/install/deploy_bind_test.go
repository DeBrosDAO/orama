package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// bindMigrationTree is a node's /etc and /var/lib/orama-deploy in a temporary
// directory.
type bindMigrationTree struct {
	m         deployBindMigration
	unitDir   string
	wantsDir  string
	secretDir string
}

func newBindMigrationTree(t *testing.T) bindMigrationTree {
	t.Helper()
	etc := t.TempDir()
	unitDir := filepath.Join(etc, "systemd", "system")
	wantsDir := filepath.Join(unitDir, "multi-user.target.wants")
	secretDir := filepath.Join(t.TempDir(), "orama-deploy")
	for _, d := range []string{wantsDir, secretDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return bindMigrationTree{
		m: deployBindMigration{
			unitRoot: rootfs.At(etc), unitDir: unitDir, wantsDir: wantsDir,
			secretRoot: rootfs.At(secretDir), secretDir: secretDir,
		},
		unitDir: unitDir, wantsDir: wantsDir, secretDir: secretDir,
	}
}

// enable links unit the way `systemctl enable` does.
func (tr bindMigrationTree) enable(t *testing.T, unit string) {
	t.Helper()
	if err := os.Symlink("/etc/systemd/system/template@.service", filepath.Join(tr.wantsDir, unit)); err != nil {
		t.Fatal(err)
	}
}

func (tr bindMigrationTree) env(t *testing.T, instance string, env map[string]string) {
	t.Helper()
	contents, err := deployments.RenderEnvFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deploysecrets.Path(tr.secretDir, instance, deploysecrets.Env), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (tr bindMigrationTree) dropIn(runtime, instance string) (string, bool) {
	data, err := os.ReadFile(privhelper.DeployBindDropInPath(tr.unitDir, runtime, instance))
	return string(data), err == nil
}

// A deployment running before the upgrade keeps listening after its next
// restart: it gets the port its environment already names.
func TestDeployBindMigration_allowsEachEnabledDeploymentItsPort(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.enable(t, "orama-deploy-node@acme-web.service")
	tr.env(t, "acme-web", map[string]string{"PORT": "10200", "NODE_ENV": "production"})
	tr.enable(t, "orama-deploy-go@acme-api.service")
	tr.env(t, "acme-api", map[string]string{"PORT": "10201"})
	// A tenant value that tries to stand in for PORT.
	tr.enable(t, "orama-deploy-npm@acme-app.service")
	tr.env(t, "acme-app", map[string]string{"A": "x\nPORT=\"10104\"", "PORT": "10202"})

	res, err := tr.m.run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []struct {
		runtime, instance string
		port              int
	}{{"node", "acme-web", 10200}, {"go", "acme-api", 10201}, {"npm", "acme-app", 10202}} {
		got, ok := tr.dropIn(want.runtime, want.instance)
		if !ok || got != privhelper.DeployBindDropIn(want.instance, want.port) {
			t.Errorf("orama-deploy-%s@%s drop-in %q, want tcp:%d", want.runtime, want.instance, got, want.port)
		}
	}
	if len(res.allowed) != 3 || len(res.skipped) != 0 {
		t.Errorf("allowed %q, skipped %q", res.allowed, res.skipped)
	}

	// An upgrade is re-run on failure; the second pass changes nothing.
	if _, err := tr.m.run(); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got, _ := tr.dropIn("node", "acme-web"); got != privhelper.DeployBindDropIn("acme-web", 10200) {
		t.Errorf("second run changed the drop-in to %q", got)
	}
}

// A port the helper would refuse is not allowed by the upgrade either: the
// deployment is reported and binds nothing until it is redeployed.
func TestDeployBindMigration_leavesAPlatformPortUnallowed(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.enable(t, "orama-deploy-node@acme-web.service")
	tr.env(t, "acme-web", map[string]string{"PORT": "10104"})
	tr.enable(t, "orama-deploy-go@acme-api.service")
	tr.env(t, "acme-api", map[string]string{"NODE_ENV": "production"}) // no PORT
	tr.enable(t, "orama-deploy-go@acme-sign.service")
	tr.env(t, "acme-sign", map[string]string{"PORT": "+10200"}) // Atoi would take it; the helper would not
	tr.enable(t, "orama-deploy-npm@acme-bad.service")
	if err := os.WriteFile(deploysecrets.Path(tr.secretDir, "acme-bad", deploysecrets.Env), []byte("PORT=10200\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := tr.m.run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, u := range []struct{ runtime, instance string }{{"node", "acme-web"}, {"go", "acme-api"}, {"go", "acme-sign"}, {"npm", "acme-bad"}} {
		if got, ok := tr.dropIn(u.runtime, u.instance); ok {
			t.Errorf("orama-deploy-%s@%s was allowed %q", u.runtime, u.instance, got)
		}
	}
	if len(res.skipped) != 4 || len(res.allowed) != 0 {
		t.Fatalf("skipped %q, allowed %q", res.skipped, res.allowed)
	}
	if !strings.Contains(strings.Join(res.skipped, "\n"), "redeployed") {
		t.Errorf("the report does not say what to do: %q", res.skipped)
	}
}

// An enabled unit with no environment file cannot start (its EnvironmentFile=
// is required); there is nothing to allow and nothing has failed.
func TestDeployBindMigration_skipsAUnitWithNoEnvironment(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.enable(t, "orama-deploy-go@gone-api.service")
	res, err := tr.m.run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.skipped) != 1 || !strings.Contains(res.skipped[0], "no environment file") {
		t.Errorf("skipped %q", res.skipped)
	}
	if _, ok := tr.dropIn("go", "gone-api"); ok {
		t.Error("a drop-in was written for a deployment with no environment")
	}
}

// Only the runtime units: the build and clean units bind nothing, and the
// namespace services are not deployments.
func TestDeployBindMigration_ignoresEverythingButRuntimeUnits(t *testing.T) {
	tr := newBindMigrationTree(t)
	for _, unit := range []string{
		"orama-deploy-build@acme-web.service",
		"orama-deploy-clean@acme-web.service",
		"orama-namespace-gateway@index.service",
		"orama-node.service",
		"orama-deploy-acme-web.service",     // a pre-template unit
		"orama-deploy-node@.hidden.service", // not a valid instance
		"orama-deploy-node@acme-web.service.d",
	} {
		tr.enable(t, unit)
	}
	tr.env(t, "acme-web", map[string]string{"PORT": "10200"})
	res, err := tr.m.run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.allowed)+len(res.skipped) != 0 {
		t.Errorf("acted on %q / %q", res.allowed, res.skipped)
	}
	if entries, _ := os.ReadDir(tr.unitDir); len(entries) != 1 {
		t.Errorf("wrote into %s: %v", tr.unitDir, entries)
	}
}

// A fresh node has no enabled units at all.
func TestDeployBindMigration_noWantsDirectory(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.m.wantsDir = filepath.Join(tr.unitDir, "missing.wants")
	if res, err := tr.m.run(); err != nil || len(res.allowed)+len(res.skipped) != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
}

// A drop-in that cannot be written fails the step, naming the unit.
func TestDeployBindMigration_failsWhenADropInCannotBeWritten(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.enable(t, "orama-deploy-node@acme-web.service")
	tr.env(t, "acme-web", map[string]string{"PORT": "10200"})
	// A symlink where the drop-in directory goes: root does not follow it.
	if err := os.Symlink(t.TempDir(), filepath.Join(tr.unitDir, "orama-deploy-node@acme-web.service.d")); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.m.run(); err == nil || !strings.Contains(err.Error(), "acme-web") {
		t.Fatalf("err = %v, want a failure naming the deployment", err)
	}
}

// A node that never staged a deployment's secrets has no secrets directory.
func TestDeployBindMigration_noSecretsDirectory(t *testing.T) {
	tr := newBindMigrationTree(t)
	tr.enable(t, "orama-deploy-node@acme-web.service")
	missing := filepath.Join(t.TempDir(), "orama-deploy")
	tr.m.secretRoot, tr.m.secretDir = rootfs.At(missing), missing
	res, err := tr.m.run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.skipped) != 1 || !strings.Contains(res.skipped[0], "no environment file") {
		t.Errorf("skipped %q", res.skipped)
	}
}
