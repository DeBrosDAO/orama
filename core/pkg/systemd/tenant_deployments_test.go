package systemd

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// seedDeployment creates a deployment directory owned by namespace/name.
func seedDeployment(t *testing.T, m *Manager, namespace, name, instance string) {
	t.Helper()
	dir := filepath.Join(m.deploymentsDir(), instance)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"namespace":"` + namespace + `","name":"` + name + `"}`
	if err := os.WriteFile(filepath.Join(dir, deployOwnerMarkerName), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newDeploymentManager is a manager whose deployments directory holds the
// deployments of "acme" and of "acme-corp", and which answers every list-units
// pattern with the loaded node-runtime unit of the instance it names.
func newDeploymentManager(t *testing.T) (*Manager, *fakeUnits) {
	t.Helper()
	m, f := newFakeManager(t)
	m.deploymentsBase = filepath.Join(t.TempDir(), "deployments")
	m.sqliteBase = filepath.Join(t.TempDir(), "sqlite")
	m.systemdDir = t.TempDir()
	seedDeployment(t, m, "acme", "web", "acme-web")
	seedDeployment(t, m, "acme-corp", "web", "acme-corp-web")
	// Ambiguous on purpose: "acme-corp-web" is also namespace "acme" with name
	// "corp-web". The owner marker decides, and it says acme-corp.
	m.listUnitsCmd = func(args ...string) ([]byte, error) {
		pattern := args[len(args)-1]
		instance := strings.TrimSuffix(strings.SplitN(pattern, "@", 2)[1], ".service")
		return []byte("orama-deploy-node@" + instance + ".service loaded active running x\n"), nil
	}
	return m, f
}

// Tearing down "acme" must not reach "acme-corp": a glob on "acme-*" matched
// both and stopped, disabled and removed the other tenant's deployments.
func TestStopDeploymentServicesForNamespace_leavesANamespaceWithTheSamePrefixAlone(t *testing.T) {
	m, f := newDeploymentManager(t)

	if err := m.StopDeploymentServicesForNamespace("acme"); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.calls, "|")
	if !strings.Contains(calls, "stop orama-deploy-node@acme-web.service") ||
		!strings.Contains(calls, "disable orama-deploy-node@acme-web.service") {
		t.Fatalf("acme's deployment was not stopped and disabled: %v", f.calls)
	}
	if strings.Contains(calls, "acme-corp") {
		t.Fatalf("a unit of acme-corp was touched: %v", f.calls)
	}
}

func TestOwnedDeploymentInstances_followsTheOwnerMarker(t *testing.T) {
	m, _ := newDeploymentManager(t)
	// No marker: owner unknown, never returned. A plain file: ignored.
	if err := os.MkdirAll(filepath.Join(m.deploymentsDir(), "acme-unmarked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.deploymentsDir(), "acme-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Owned by acme although its instance starts with "acme-corp-".
	seedDeployment(t, m, "acme", "corp-api", "acme-corp-api")

	got, err := m.OwnedDeploymentInstances("acme")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"acme-corp-api", "acme-web"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOwnedDeploymentInstances_noDeploymentsDirectory(t *testing.T) {
	m, _ := newFakeManager(t)
	m.deploymentsBase = filepath.Join(t.TempDir(), "missing")

	got, err := m.OwnedDeploymentInstances("acme")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want nothing and no error", got, err)
	}
}

func TestOwnedDeploymentInstances_corruptMarkerIsAnError(t *testing.T) {
	m, _ := newFakeManager(t)
	m.deploymentsBase = t.TempDir()
	dir := filepath.Join(m.deploymentsBase, "acme-web")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, deployOwnerMarkerName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.OwnedDeploymentInstances("acme"); err == nil {
		t.Fatal("an unreadable owner marker was skipped: its deployment could survive the teardown unnoticed")
	}
}

// A deployment unit that cannot be stopped is reported, so the namespace's
// data is kept.
func TestStopDeploymentServicesForNamespace_reportsAUnitThatWillNotStop(t *testing.T) {
	m, _ := newDeploymentManager(t)
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		return []byte("Job failed"), errors.New("exit status 1")
	}
	m.unitState = func(string) (unitState, error) { return unitState{Load: "loaded", Active: "active"}, nil }

	err := m.StopDeploymentServicesForNamespace("acme")
	if err == nil || !strings.Contains(err.Error(), "acme-web") {
		t.Fatalf("err = %v, want the stuck deployment unit", err)
	}
}

func TestStopDeploymentServicesForNamespace_reportsAFailedListing(t *testing.T) {
	m, _ := newDeploymentManager(t)
	m.listUnitsCmd = func(...string) ([]byte, error) { return []byte("boom"), errors.New("exit status 1") }

	if err := m.StopDeploymentServicesForNamespace("acme"); err == nil {
		t.Fatal("a failed unit listing was swallowed")
	}
}

// RemoveTenantData (T6): the namespace's SQLite databases and its own
// deployment directories go; another namespace's, and unmarked directories, stay.
func TestRemoveTenantData_removesOnlyTheNamespacesOwnData(t *testing.T) {
	m, _ := newDeploymentManager(t)
	for _, ns := range []string{"acme", "acme-corp"} {
		if err := os.MkdirAll(filepath.Join(m.sqliteDir(), ns), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.sqliteDir(), ns, "db.db"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unmarked := filepath.Join(m.deploymentsDir(), "acme-unmarked")
	if err := os.MkdirAll(unmarked, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.RemoveTenantData("acme"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		filepath.Join(m.sqliteDir(), "acme"):               false,
		filepath.Join(m.deploymentsDir(), "acme-web"):      false,
		filepath.Join(m.sqliteDir(), "acme-corp"):          true,
		filepath.Join(m.deploymentsDir(), "acme-corp-web"): true,
		unmarked: true,
	} {
		_, err := os.Stat(path)
		if exists := err == nil; exists != want {
			t.Errorf("%s exists = %v, want %v", path, exists, want)
		}
	}
}

func TestRemoveTenantData_refusesANameThatIsNotOneDirectory(t *testing.T) {
	m, _ := newDeploymentManager(t)
	sentinel := filepath.Join(filepath.Dir(m.sqliteDir()), "keep")
	if err := os.MkdirAll(sentinel, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{"", ".", "..", "../keep", "a/b"} {
		if err := m.RemoveTenantData(ns); err == nil {
			t.Errorf("%q was accepted", ns)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("a path outside the tenant directories was removed")
	}
}

func TestRemoveTenantData_nothingToRemoveIsNotAnError(t *testing.T) {
	m, _ := newFakeManager(t)
	m.deploymentsBase = filepath.Join(t.TempDir(), "none")
	m.sqliteBase = filepath.Join(t.TempDir(), "none")

	if err := m.RemoveTenantData("acme"); err != nil {
		t.Fatal(err)
	}
}

// T7: a stop or disable that fails is judged by systemd's state, not by the
// wording of its output.
func TestStopService_judgesAFailureBySystemdState(t *testing.T) {
	cases := map[string]struct {
		output  string
		state   unitState
		stateEr error
		wantErr bool
	}{
		"not loaded":                           {"Unit x not loaded.", unitState{"not-found", "inactive"}, nil, false},
		"inactive":                             {"boom", unitState{"loaded", "inactive"}, nil, false},
		"failed":                               {"boom", unitState{"loaded", "failed"}, nil, false},
		"still active though it says inactive": {"Failed: unit is not inactive", unitState{"loaded", "active"}, nil, true},
		"still activating":                     {"timeout", unitState{"loaded", "activating"}, nil, true},
		"state unreadable":                     {"boom", unitState{}, errors.New("no bus"), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := newFakeManager(t)
			m.runUnitCmd = func(...string) ([]byte, error) { return []byte(tc.output), errors.New("exit status 1") }
			m.unitState = func(string) (unitState, error) { return tc.state, tc.stateEr }
			if err := m.StopService("acme", ServiceTypeRQLite); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDisableService_judgesAFailureBySystemdState(t *testing.T) {
	cases := map[string]struct {
		output  string
		state   unitState
		stateEr error
		wantErr bool
	}{
		"no such unit":                {"Failed to disable unit: Unit file x does not exist.", unitState{"not-found", "inactive"}, nil, false},
		"loaded and will not disable": {"Failed to disable unit: not loaded by policy", unitState{"loaded", "active"}, nil, true},
		"state unreadable":            {"boom", unitState{}, errors.New("no bus"), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := newFakeManager(t)
			m.runUnitCmd = func(...string) ([]byte, error) { return []byte(tc.output), errors.New("exit status 1") }
			m.unitState = func(string) (unitState, error) { return tc.state, tc.stateEr }
			if err := m.DisableService("acme", ServiceTypeRQLite); (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestParseUnitState(t *testing.T) {
	got, err := parseUnitState("LoadState=loaded\nActiveState=active\n")
	if err != nil || got != (unitState{"loaded", "active"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := parseUnitState("ActiveState=active\n"); err == nil {
		t.Fatal("a reply without LoadState was accepted")
	}
	if _, err := parseUnitState(""); err == nil {
		t.Fatal("an empty reply was accepted")
	}
}
