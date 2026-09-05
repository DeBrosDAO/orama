package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTestManager(t *testing.T, systemdDir string) *Manager {
	t.Helper()
	return &Manager{logger: zap.NewNop(), systemdDir: systemdDir}
}

// A missing template must fail, and the error must name it.
//
// The two CLI copies this replaced logged a warning and continued, so a node
// missing half its template units finished installing and reported success —
// and only failed later, as "Unit orama-namespace-<x>@index.service not found"
// from a supervisor that then exited.
func TestInstallTemplateUnits_missing_template_fails_by_name(t *testing.T) {
	source := t.TempDir()
	dest := t.TempDir()

	units := UnitFilesToInstall()
	if len(units) < 2 {
		t.Fatalf("expected several template units, got %d", len(units))
	}
	// Everything present except the last one.
	missing := units[len(units)-1]
	for _, u := range units[:len(units)-1] {
		if err := os.WriteFile(filepath.Join(source, u), []byte("[Unit]\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	err := newTestManager(t, dest).InstallTemplateUnits(source)
	if err == nil {
		t.Fatal("a missing template installed successfully")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error does not name the missing template %q: %v", missing, err)
	}
}

// An empty source directory is total failure and must be reported as such. The
// old code's installed-count reached zero, skipped the daemon reload, and
// returned nil.
func TestInstallTemplateUnits_empty_source_fails(t *testing.T) {
	if err := newTestManager(t, t.TempDir()).InstallTemplateUnits(t.TempDir()); err == nil {
		t.Fatal("an empty source directory installed successfully")
	}
}

// An unwritable destination must fail too — the other error the copies skipped.
func TestInstallTemplateUnits_unwritable_destination_fails(t *testing.T) {
	source := t.TempDir()
	for _, u := range UnitFilesToInstall() {
		if err := os.WriteFile(filepath.Join(source, u), []byte("[Unit]\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	dest := filepath.Join(t.TempDir(), "read-only")
	if err := os.Mkdir(dest, 0500); err != nil {
		t.Fatal(err)
	}

	err := newTestManager(t, dest).InstallTemplateUnits(source)
	if err == nil {
		t.Fatal("writing to a read-only directory reported success")
	}
	if !strings.Contains(err.Error(), "write template") {
		t.Fatalf("error does not identify the write failure: %v", err)
	}
}

// A deployment's unit is a template installed with the release. If it is not on
// the install list it is not on the node, and the first tenant deploy after an
// upgrade fails with "Unit orama-deploy-node@… not found".
func TestUnitFilesToInstall_includesTheDeploymentTemplates(t *testing.T) {
	installed := map[string]bool{}
	for _, unit := range UnitFilesToInstall() {
		installed[unit] = true
	}
	for _, unit := range DeploymentTemplateUnits {
		if !installed[unit] {
			t.Errorf("%s is a deployment template and is never installed", unit)
		}
	}
}

// And it has to exist in the tree that ships, or the install fails outright.
func TestDeploymentTemplateUnits_shipWithTheRelease(t *testing.T) {
	root := repoRootDir(t)
	for _, unit := range DeploymentTemplateUnits {
		path := filepath.Join(root, "systemd", unit)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is on the install list and not in the tree: %v", unit, err)
		}
	}
}

// repoRootDir walks up to the directory holding go.mod.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
