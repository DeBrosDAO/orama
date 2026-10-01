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
	want := "stop orama-namespace-gateway@acme.service|disable --no-reload orama-namespace-gateway@acme.service"
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
		if unit, ok := strings.CutPrefix(c, "disable --no-reload "); ok {
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
		if args[0] == "disable" && strings.Contains(args[len(args)-1], "olric") {
			return []byte("Failed to disable unit"), errors.New("exit status 1")
		}
		return nil, nil
	}

	err := m.TeardownAllNamespaceServices("acme")
	if err == nil || !strings.Contains(err.Error(), "orama-namespace-olric@acme.service") {
		t.Fatalf("err = %v, want the olric disable failure", err)
	}
	if !strings.Contains(strings.Join(f.calls, "|"), "disable --no-reload orama-namespace-rqlite@acme.service") {
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

// WebRTC turned off on a namespace that stays: the unit is stopped, disabled
// and its env file removed, so the upgrade restart finds nothing to start.
// Other services of the namespace are untouched.
func TestTeardownServiceAndEnv_disablesAndRemovesTheEnv(t *testing.T) {
	m, f := newFakeManager(t)
	owner := unitenv.Owner{UID: os.Getuid(), GID: os.Getgid()}
	for _, svc := range []string{"sfu", "gateway"} {
		if err := unitenv.Write(m.unitEnvDir, "acme", svc, []byte("A=1\n"), owner); err != nil {
			t.Fatal(err)
		}
	}
	m.clearUnitEnv = func(ns, svc string) error { return unitenv.Clear(m.unitEnvDir, ns, svc) }

	if err := m.TeardownServiceAndEnv("acme", ServiceTypeSFU); err != nil {
		t.Fatal(err)
	}
	want := "stop orama-namespace-sfu@acme.service|disable --no-reload orama-namespace-sfu@acme.service"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if _, err := os.Stat(unitenv.Path(m.unitEnvDir, "acme", "sfu")); !os.IsNotExist(err) {
		t.Error("the sfu env file survived: the next upgrade would restart it")
	}
	if _, err := os.Stat(unitenv.Path(m.unitEnvDir, "acme", "gateway")); err != nil {
		t.Error("the gateway's env file was removed")
	}
}

// The env file is the retry handle: kept when the unit could not be disabled.
func TestTeardownServiceAndEnv_keepsTheEnvWhenTheUnitCannotBeDisabled(t *testing.T) {
	m, _ := newFakeManager(t)
	owner := unitenv.Owner{UID: os.Getuid(), GID: os.Getgid()}
	if err := unitenv.Write(m.unitEnvDir, "acme", "sfu", []byte("A=1\n"), owner); err != nil {
		t.Fatal(err)
	}
	cleared := false
	m.clearUnitEnv = func(string, string) error { cleared = true; return nil }
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		if args[0] == "disable" {
			return []byte("Failed"), errors.New("exit status 1")
		}
		return nil, nil
	}

	if err := m.TeardownServiceAndEnv("acme", ServiceTypeSFU); err == nil {
		t.Fatal("a failed disable was not reported")
	}
	if cleared {
		t.Fatal("the env file was removed although the unit is still enabled")
	}
}

func TestTeardownServiceAndEnv_reportsAnEnvRemovalFailure(t *testing.T) {
	m, _ := newFakeManager(t)
	m.clearUnitEnv = func(string, string) error { return errors.New("helper refused") }
	if err := m.TeardownServiceAndEnv("acme", ServiceTypeSFU); err == nil || !strings.Contains(err.Error(), "helper refused") {
		t.Fatalf("err = %v", err)
	}
}

// systemd keeps listing an instance after it was stopped and disabled. Such a
// unit is not a namespace on this node: counting it made the orphan sweep tear
// the same removed namespace down on every pass, spending its per-sweep cap.
// A running unit, or a stopped but enabled one (it starts at boot), counts.
func TestLocalTenantNamespaces_aStoppedDisabledUnitIsNotANamespace(t *testing.T) {
	m, _ := newFakeManager(t)
	m.listUnitsCmd = func(...string) ([]byte, error) {
		return []byte(strings.Join([]string{
			"orama-namespace-rqlite@gone.service loaded inactive dead Orama Namespace RQLite (gone)",
			"orama-namespace-rqlite@running.service loaded active running Orama Namespace RQLite (running)",
			"orama-namespace-olric@boots.service loaded inactive dead Orama Namespace Olric (boots)",
			"orama-namespace-gateway@crashed.service loaded failed failed Orama Namespace Gateway (crashed)",
		}, "\n")), nil
	}
	states := map[string]unitState{
		"orama-namespace-rqlite@gone.service":     {Load: "loaded", Active: "inactive", UnitFile: "disabled"},
		"orama-namespace-rqlite@running.service":  {Load: "loaded", Active: "active", UnitFile: "enabled"},
		"orama-namespace-olric@boots.service":     {Load: "loaded", Active: "inactive", UnitFile: "enabled"},
		"orama-namespace-gateway@crashed.service": {Load: "loaded", Active: "failed", UnitFile: "disabled"},
	}
	m.unitState = func(u string) (unitState, error) { return states[u], nil }

	got, err := m.LocalTenantNamespaces()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"boots", "running"}) {
		t.Fatalf("got %v, want [boots running]: a stopped, disabled unit is not a namespace", got)
	}
}

func TestLocalTenantNamespaces_anUnreadableUnitStateIsAnError(t *testing.T) {
	m, _ := newFakeManager(t)
	m.listUnitsCmd = func(...string) ([]byte, error) {
		return []byte("orama-namespace-rqlite@x.service loaded active running x\n"), nil
	}
	m.unitState = func(string) (unitState, error) { return unitState{}, errors.New("systemctl show failed") }
	if _, err := m.LocalTenantNamespaces(); err == nil {
		t.Fatal("an unreadable unit state was treated as an answer")
	}
}

// A disable reloads systemd unless told not to, and a namespace's five
// services made five reloads. Every disable passes --no-reload, and the
// namespace is reloaded once at the end.
func TestTeardownAllNamespaceServices_reloadsOnceNotPerService(t *testing.T) {
	m, f := newFakeManager(t)

	if err := m.TeardownAllNamespaceServices("acme"); err != nil {
		t.Fatal(err)
	}
	reloads, disables := 0, 0
	for _, c := range f.calls {
		switch {
		case c == "daemon-reload":
			reloads++
		case strings.HasPrefix(c, "disable "):
			disables++
			if !strings.HasPrefix(c, "disable --no-reload orama-namespace-") {
				t.Errorf("disable without --no-reload: %q", c)
			}
		}
	}
	if disables != len(tenantTeardownOrder) {
		t.Errorf("disables = %d, want %d (calls %v)", disables, len(tenantTeardownOrder), f.calls)
	}
	if reloads != 1 || f.calls[len(f.calls)-1] != "daemon-reload" {
		t.Errorf("want exactly one daemon-reload, last; calls %v", f.calls)
	}
}

func TestTeardownAllNamespaceServices_reportsAFailedReload(t *testing.T) {
	m, f := newFakeManager(t)
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		if args[0] == "daemon-reload" {
			return []byte("Failed to reload"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if err := m.TeardownAllNamespaceServices("acme"); err == nil || !strings.Contains(err.Error(), "reload systemd") {
		t.Fatalf("err = %v, want the reload failure", err)
	}
}
