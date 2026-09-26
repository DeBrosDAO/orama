package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/legacylayout"
)

type recordingStager struct{ unitEnvs []string }

func (r *recordingStager) SetUnitEnv(namespace, service, _ string) error {
	r.unitEnvs = append(r.unitEnvs, namespace+"/"+service)
	return nil
}
func (r *recordingStager) SetDeploymentEnv(string, string) error   { return nil }
func (r *recordingStager) SetDeploymentToken(string, string) error { return nil }

// useTestLegacyLayout points the migration at a temp unit env tree and stager.
func useTestLegacyLayout(t *testing.T, stager legacylayout.Stager) {
	t.Helper()
	old := legacyLayout
	legacyLayout = legacylayout.Migrator{UnitEnvDir: filepath.Join(t.TempDir(), "unit-env"), Stager: stager}
	t.Cleanup(func() { legacyLayout = old })
}

// Nothing that starts or regenerates a namespace service, or reads what a
// gateway wrote, may run before the old layout has been moved.
func TestBootComponents_everythingWaitsForTheLegacyLayout(t *testing.T) {
	deps := map[string][]string{}
	for _, c := range (&Node{}).bootComponents() {
		deps[c.Name] = c.DependsOn
	}
	var reaches func(name string) bool
	reaches = func(name string) bool {
		for _, d := range deps[name] {
			if d == compLegacyLayout || reaches(d) {
				return true
			}
		}
		return false
	}
	for name := range deps {
		if name == compDataDir || name == compLegacyLayout {
			continue
		}
		if !reaches(name) {
			t.Errorf("component %q can start before %q", name, compLegacyLayout)
		}
	}
}

func TestMigrateLegacyLayout_movesTheNodesOwnOramaDir(t *testing.T) {
	oramaDir := t.TempDir()
	legacyEnv := filepath.Join(oramaDir, "data", "namespaces", "acme", "rqlite.env")
	for path, body := range map[string]string{
		legacyEnv: "HTTP_ADDR=10.0.0.1:10200\n",
		filepath.Join(oramaDir, "configs", "turn.yaml"): "turn",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stager := &recordingStager{}
	useTestLegacyLayout(t, stager)

	n := testNode(t)
	n.config = &config.Config{}
	n.config.Node.DataDir = filepath.Join(oramaDir, "data")
	if err := n.migrateLegacyLayout(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oramaDir, "data", "turn", "turn.yaml")); err != nil {
		t.Errorf("the TURN config was not moved under data/: %v", err)
	}
	if len(stager.unitEnvs) != 1 || stager.unitEnvs[0] != "acme/rqlite" {
		t.Errorf("staged %v, want acme/rqlite", stager.unitEnvs)
	}
}

func TestMigrateLegacyLayout_aConflictHoldsTheNode(t *testing.T) {
	useTestLegacyLayout(t, &recordingStager{})
	oramaDir := t.TempDir()
	for _, rel := range []string{"configs/turn.yaml", "data/turn/turn.yaml"} {
		path := filepath.Join(oramaDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n := testNode(t)
	n.config = &config.Config{}
	n.config.Node.DataDir = filepath.Join(oramaDir, "data")
	err := n.migrateLegacyLayout(context.Background())
	if err == nil || !strings.Contains(err.Error(), "configs/turn.yaml") {
		t.Fatalf("a path on both layouts must fail the component, naming it: %v", err)
	}
}
