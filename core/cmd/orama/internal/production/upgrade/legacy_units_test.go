package upgrade

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The per-deployment units 0.122.x wrote are stopped, disabled and deleted;
// the templates and everything else in the directory are not touched.
func TestRetireLegacyDeploymentUnits(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"orama-deploy-acme-web.service", "orama-deploy-node@.service", "orama-node.service"} {
		writeTestFile(t, filepath.Join(dir, name), "[Unit]\n")
	}
	if err := os.Symlink(devNull, filepath.Join(dir, "orama-deploy-masked-app.service")); err != nil {
		t.Fatal(err)
	}
	var calls []string
	err := retireLegacyDeploymentUnitsIn(dir, func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stop orama-deploy-acme-web.service", "disable orama-deploy-acme-web.service",
		"stop orama-deploy-masked-app.service", "disable orama-deploy-masked-app.service", "unmask orama-deploy-masked-app.service",
		"daemon-reload",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("systemctl calls %v, want %v", calls, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "orama-deploy-acme-web.service")); !os.IsNotExist(err) {
		t.Error("the unit file was not removed")
	}
	for _, kept := range []string{"orama-deploy-node@.service", "orama-node.service"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s was touched: %v", kept, err)
		}
	}
}

// A symlink under a legacy name that is not a mask was not written by any
// release; nothing is removed.
func TestRetireLegacyDeploymentUnits_refusesAForeignSymlink(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "orama-deploy-a.service"), "[Unit]\n")
	if err := os.Symlink("/etc/shadow", filepath.Join(dir, "orama-deploy-b.service")); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := retireLegacyDeploymentUnitsIn(dir, func(...string) error { called = true; return nil }); err == nil {
		t.Fatal("a foreign symlink was accepted")
	}
	if called {
		t.Error("systemctl ran before the directory was checked")
	}
}

func TestRetireLegacyDeploymentUnits_noneIsNothing(t *testing.T) {
	called := false
	if err := retireLegacyDeploymentUnitsIn(t.TempDir(), func(...string) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("systemctl ran with nothing to retire")
	}
}
