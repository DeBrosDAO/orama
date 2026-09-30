package systemd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// A restart stops a unit and leaves it enabled; teardown must not.
func TestStopService_keepsTheUnitEnabled(t *testing.T) {
	m, f := newFakeManager(t)

	if err := m.StopService("acme", ServiceTypeRQLite); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, "|"); got != "stop orama-namespace-rqlite@acme.service" {
		t.Fatalf("calls = %q, want a bare stop: a restart must leave the unit enabled", got)
	}
}

func TestTeardownService_stopsThenDisables(t *testing.T) {
	m, f := newFakeManager(t)

	if err := m.TeardownService("acme", ServiceTypeGateway); err != nil {
		t.Fatal(err)
	}
	want := "stop orama-namespace-gateway@acme.service|disable orama-namespace-gateway@acme.service"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

// Every service a tenant can run is stopped AND disabled, including the
// WebRTC ones: an enabled sfu unit is restarted by the next upgrade as surely
// as an rqlite.
func TestTeardownAllNamespaceServices_disablesEveryTenantService(t *testing.T) {
	m, f := newFakeManager(t)

	if err := m.TeardownAllNamespaceServices("acme"); err != nil {
		t.Fatal(err)
	}
	disabled := map[string]bool{}
	for _, c := range f.calls {
		if unit, ok := strings.CutPrefix(c, "disable "); ok {
			disabled[unit] = true
		}
	}
	for _, st := range []ServiceType{ServiceTypeRQLite, ServiceTypeOlric, ServiceTypeGateway, ServiceTypeSFU, ServiceTypeTURN} {
		unit := m.serviceName("acme", st)
		if !disabled[unit] {
			t.Errorf("%s was not disabled (calls %v)", unit, f.calls)
		}
	}
	// Dependents first.
	if strings.Index(strings.Join(f.calls, "|"), "gateway@acme") > strings.Index(strings.Join(f.calls, "|"), "rqlite@acme") {
		t.Errorf("the gateway must be stopped before rqlite: %v", f.calls)
	}
}

// A unit that could not be disabled is a unit that comes back: the failure is
// returned, and the other services are still torn down.
func TestTeardownAllNamespaceServices_reportsAFailedDisableAndContinues(t *testing.T) {
	m, f := newFakeManager(t)
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		if args[0] == "disable" && strings.Contains(args[1], "olric") {
			return []byte("Failed to disable unit"), errors.New("exit status 1")
		}
		return nil, nil
	}

	err := m.TeardownAllNamespaceServices("acme")
	if err == nil || !strings.Contains(err.Error(), "orama-namespace-olric@acme.service") {
		t.Fatalf("err = %v, want the olric disable failure", err)
	}
	if !strings.Contains(strings.Join(f.calls, "|"), "disable orama-namespace-rqlite@acme.service") {
		t.Fatalf("rqlite was not torn down after olric failed: %v", f.calls)
	}
}

func TestTenantNamespaceFromUnit(t *testing.T) {
	tests := map[string]struct {
		ns string
		ok bool
	}{
		"orama-namespace-rqlite@acme.service":   {"acme", true},
		"orama-namespace-gateway@my-ns.service": {"my-ns", true},
		"orama-namespace-sfu@acme.service":      {"acme", true},
		// Not tenant services: the node's own stack.
		"orama-namespace-wireguard@index.service": {"", false},
		"orama-namespace-ipfs@index.service":      {"", false},
		"orama-node.service":                      {"", false},
		"orama-namespace-rqlite@.service":         {"", false},
		"orama-namespace-rqlite@../x.service":     {"", false},
		"":                                        {"", false},
	}
	for unit, want := range tests {
		ns, ok := TenantNamespaceFromUnit(unit)
		if ns != want.ns || ok != want.ok {
			t.Errorf("TenantNamespaceFromUnit(%q) = %q, %v; want %q, %v", unit, ns, ok, want.ns, want.ok)
		}
	}
}

// What a restart would start: a namespace directory with a provisioned tenant
// service. A directory with nothing provisioned, and a file, are not.
func TestTenantNamespacesOnDisk(t *testing.T) {
	m, _ := newFakeManager(t)
	for _, ns := range []string{"alpha", "beta", "empty"} {
		if err := os.MkdirAll(filepath.Join(m.namespaceBase, ns), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.namespaceBase, "afile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	owner := unitenv.Owner{UID: os.Getuid(), GID: os.Getgid()}
	for ns, svc := range map[string]string{"alpha": "rqlite", "beta": "gateway"} {
		if err := unitenv.Write(m.unitEnvDir, ns, svc, []byte("A=1\n"), owner); err != nil {
			t.Fatal(err)
		}
	}

	got, err := m.tenantNamespacesOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("got %v, want [alpha beta]", got)
	}
}

func TestTenantNamespacesOnDisk_noNamespacesDirectory(t *testing.T) {
	m, _ := newFakeManager(t)
	m.namespaceBase = filepath.Join(t.TempDir(), "missing")

	got, err := m.tenantNamespacesOnDisk()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want nothing and no error", got, err)
	}
}
