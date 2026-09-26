package lifecycle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envLayout is a node's namespaces dir and unit env tree in a temp dir.
type envLayout struct{ namespaces, unitEnv string }

func newEnvLayout(t *testing.T) envLayout {
	t.Helper()
	root := t.TempDir()
	return envLayout{namespaces: filepath.Join(root, "data", "namespaces"), unitEnv: filepath.Join(root, "unit-env")}
}

// hostsRQLite makes ns a namespace this node serves (cluster-state.json) with
// an rqlite data directory here.
func (l envLayout) hostsRQLite(t *testing.T, ns string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(l.namespaces, ns, "rqlite", "node-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.namespaces, ns, "cluster-state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Once the unit env tree exists it is where the envs are; the old files are
// not consulted.
func TestTenantRQLiteEndpoints_readsTheUnitEnvTree(t *testing.T) {
	l := newEnvLayout(t)
	l.hostsRQLite(t, "anchat")
	writeEnv(t, l.unitEnv, "anchat", "HTTP_ADDR=10.0.0.1:10200\n")
	writeEnv(t, l.unitEnv, "index", "HTTP_ADDR=10.0.0.1:10100\n")
	writeEnv(t, l.namespaces, "anchat", "HTTP_ADDR=10.0.0.1:19999\n")

	got, failures, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t))
	if err != nil || len(failures) != 0 {
		t.Fatalf("err %v failures %v", err, failures)
	}
	if len(got) != 1 || got["anchat"].HostPort() != "10.0.0.1:10200" {
		t.Fatalf("endpoints %v, want only anchat from the unit env tree", got)
	}
}

// The first upgrade from the old layout runs before orama-node has moved the
// env files. Reading only the new tree found nothing and skipped every
// tenant's leadership transfer.
func TestTenantRQLiteEndpoints_firstUpgradeReadsTheOldLayout(t *testing.T) {
	l := newEnvLayout(t)
	l.hostsRQLite(t, "anchat")
	writeEnv(t, l.namespaces, "anchat", "HTTP_ADDR=10.0.0.1:10200\n")

	got, _, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if got["anchat"].HostPort() != "10.0.0.1:10200" {
		t.Fatalf("endpoints %v, want anchat from data/namespaces/anchat/rqlite.env", got)
	}
}

// A namespace with rqlite data here and no env in the tree read cannot have
// its leader handed over; that fails the step instead of being skipped.
func TestTenantRQLiteEndpoints_rqliteDataWithoutEnvFails(t *testing.T) {
	l := newEnvLayout(t)
	l.hostsRQLite(t, "anchat")
	l.hostsRQLite(t, "beta")
	writeEnv(t, l.unitEnv, "anchat", "HTTP_ADDR=10.0.0.1:10200\n")

	_, _, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t))
	if err == nil || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("expected a failure naming beta, got %v", err)
	}
	if strings.Contains(err.Error(), "anchat") {
		t.Errorf("anchat has its env and must not be reported: %v", err)
	}
}

// Namespaces with no rqlite voter here (olric or gateway only), and the index,
// need no tenant rqlite env.
func TestTenantRQLiteEndpoints_namespacesWithoutRQLiteDataAreFine(t *testing.T) {
	l := newEnvLayout(t)
	if err := os.MkdirAll(filepath.Join(l.namespaces, "olric-only", "olric"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.namespaces, "index", "gateway"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, failures, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t))
	if err != nil || len(got) != 0 || len(failures) != 0 {
		t.Fatalf("got %v, %v, %v", got, failures, err)
	}
}

// An env that exists but cannot be addressed is reported per namespace, as
// before, not as a missing env.
func TestTenantRQLiteEndpoints_unaddressableEnvIsAFailureNotMissing(t *testing.T) {
	l := newEnvLayout(t)
	l.hostsRQLite(t, "anchat")
	writeEnv(t, l.unitEnv, "anchat", "NODE_ID=n1\n")

	_, failures, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t))
	if err != nil {
		t.Fatal(err)
	}
	if failures["anchat"] == nil {
		t.Fatalf("anchat must be reported as unaddressable: %v", failures)
	}
}

func TestTenantRQLiteEndpoints_emptyNodeAndBadTree(t *testing.T) {
	l := newEnvLayout(t)
	if got, _, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t)); err != nil || len(got) != 0 {
		t.Fatalf("a node with no namespaces: %v, %v", got, err)
	}
	if err := os.MkdirAll(filepath.Dir(l.unitEnv), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.unitEnv, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t)); err == nil {
		t.Fatal("a unit env path that is not a directory must be an error")
	}
}

// Data an unfinished teardown left behind (no cluster-state.json) runs
// nothing: it has no leader to hand over and must not block the upgrade.
func TestTenantRQLiteEndpoints_leftoverDataWithoutClusterStateIsIgnored(t *testing.T) {
	l := newEnvLayout(t)
	if err := os.MkdirAll(filepath.Join(l.namespaces, "stranded", "rqlite", "node-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tenantRQLiteEndpoints(l.unitEnv, l.namespaces, indexEndpoint(t)); err != nil {
		t.Fatalf("leftover data must not fail the step: %v", err)
	}
}
