package node

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/node/boot"
)

func TestBootComponents_clusterStartsWireGuardRQLiteAndGateway(t *testing.T) {
	n := newGraphNode(t)
	got := map[string]bool{}
	for _, c := range mustBootComponents(t, n) {
		got[c.Name] = true
	}
	for _, name := range []string{compWireGuard, compRQLiteLocal, compGateway} {
		if !got[name] {
			t.Errorf("cluster graph does not start %s", name)
		}
	}
	// Olric has no component of its own. startRQLiteLocal is what brings it up,
	// so the cluster graph starts it by including rqlite-local.
	if !got[compRQLiteLocal] {
		t.Error("cluster graph does not include rqlite-local, so it would not start Olric")
	}
}

func TestBootComponents_globalDoesNotStartClusterServices(t *testing.T) {
	n := newGraphNode(t)
	n.config.Node.Role = string(boot.RoleGlobal)

	components := mustBootComponents(t, n)
	if len(components) == 0 {
		t.Fatal("global graph is empty")
	}
	forbidden := map[string]bool{
		compWireGuard:     true,
		compRQLiteLocal:   true,
		compRQLiteCluster: true,
		compGateway:       true,
	}
	for _, c := range components {
		if forbidden[c.Name] {
			t.Errorf("global graph starts %s", c.Name)
		}
	}
	// rqlite-local is the only component whose reconcile starts Olric.
	for _, c := range components {
		if c.Name == compRQLiteLocal {
			t.Fatal("global graph includes rqlite-local, which starts Olric")
		}
	}

	sup := boot.New(nil, boot.Options{})
	if err := n.registerComponents(sup); err != nil {
		t.Fatal(err)
	}
	if err := sup.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBootComponents_explicitClusterRoleMatchesTheDefault(t *testing.T) {
	bare := newGraphNode(t)
	named := newGraphNode(t)
	named.config.Node.Role = string(boot.RoleCluster)

	bareNames := componentNames(mustBootComponents(t, bare))
	namedNames := componentNames(mustBootComponents(t, named))
	if strings.Join(bareNames, ",") != strings.Join(namedNames, ",") {
		t.Fatalf("empty role graph %v, cluster role graph %v", bareNames, namedNames)
	}
}

func TestBootComponents_bothIsRefusedWithoutTheNetnsLayout(t *testing.T) {
	n := newGraphNode(t)
	n.config.Node.Role = "both"
	_, err := n.bootComponents()
	if err == nil || !strings.Contains(err.Error(), "both") || !strings.Contains(err.Error(), "--colocated") {
		t.Fatalf("bootComponents error = %v, want one that refuses both and says how to install the layout", err)
	}
}

func TestBootComponents_bothRunsTheClusterGraphWithTheLayout(t *testing.T) {
	var checked string
	restore := verifyNetnsLayout
	verifyNetnsLayout = func(recorded string) error { checked = recorded; return nil }
	t.Cleanup(func() { verifyNetnsLayout = restore })

	orama := t.TempDir()
	if err := os.WriteFile(filepath.Join(orama, "preferences.yaml"), []byte("role: both\nglobal_netns: orama-global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := newGraphNode(t)
	n.config.Node.DataDir = filepath.Join(orama, "data")
	both := mustBootComponents(t, n)
	if checked != "orama-global" {
		t.Errorf("the layout check got %q, want the recorded namespace", checked)
	}
	cluster := newGraphNode(t)
	cluster.config.Node.Role = "cluster"
	if len(both) != len(mustBootComponents(t, cluster)) {
		t.Errorf("both starts %d components, cluster %d", len(both), len(mustBootComponents(t, cluster)))
	}
}

func TestBootComponents_bothIsRefusedWhenTheLayoutIsIncomplete(t *testing.T) {
	restore := verifyNetnsLayout
	verifyNetnsLayout = func(string) error { return errors.New("orama-global-netns.service is missing") }
	t.Cleanup(func() { verifyNetnsLayout = restore })
	n := newGraphNode(t)
	n.config.Node.Role = "both"
	if _, err := n.bootComponents(); err == nil || !strings.Contains(err.Error(), "netns.service is missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestBootComponents_preferencesRoleSelectsTheGraph(t *testing.T) {
	orama := t.TempDir()
	if err := os.WriteFile(filepath.Join(orama, "preferences.yaml"), []byte("role: global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := newGraphNode(t)
	n.config.Node.DataDir = filepath.Join(orama, "data")

	for _, c := range mustBootComponents(t, n) {
		if c.Name == compWireGuard || c.Name == compRQLiteLocal || c.Name == compGateway {
			t.Fatalf("preferences role global still starts %s", c.Name)
		}
	}
}

func TestBootComponents_configAndPreferencesMustAgree(t *testing.T) {
	orama := t.TempDir()
	if err := os.WriteFile(filepath.Join(orama, "preferences.yaml"), []byte("role: global\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := newGraphNode(t)
	n.config.Node.DataDir = filepath.Join(orama, "data")
	n.config.Node.Role = "cluster"

	_, err := n.bootComponents()
	if err == nil || !strings.Contains(err.Error(), "disagrees") {
		t.Fatalf("bootComponents error = %v, want a disagreement", err)
	}
}

func TestBootComponents_brokenPreferencesIsAnError(t *testing.T) {
	orama := t.TempDir()
	if err := os.WriteFile(filepath.Join(orama, "preferences.yaml"), []byte("role: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := newGraphNode(t)
	n.config.Node.DataDir = filepath.Join(orama, "data")

	if _, err := n.bootComponents(); err == nil {
		t.Fatal("unreadable preferences were treated as a role")
	}
}

func componentNames(components []boot.Component) []string {
	names := make([]string, len(components))
	for i, c := range components {
		names[i] = c.Name
	}
	return names
}
