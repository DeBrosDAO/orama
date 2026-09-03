package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unitDir locates core/systemd by walking up to the module root, so it cannot
// accidentally match this package's own directory.
func unitDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "systemd")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

func readUnit(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(unitDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// PartOf propagates stop AND restart. With it, `orama node restart` tore the
// overlay down and brought it back, severing every namespace raft and Olric
// memberlist on the node. The mesh is infrastructure: it outlives the
// supervisor that manages the services riding on it.
func TestWireGuardUnitIsNotPartOfTheSupervisor(t *testing.T) {
	unit := readUnit(t, "orama-namespace-wireguard@.service")

	for _, line := range strings.Split(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "PartOf=") {
			t.Errorf("WireGuard unit declares %q; a supervisor restart must not bounce wg0", trimmed)
		}
		// An ExecStop that runs wg-quick down would defeat the same guarantee
		// from the other direction.
		if strings.HasPrefix(trimmed, "ExecStop=") && strings.Contains(trimmed, "wg-quick down") {
			t.Errorf("WireGuard unit declares %q; stopping the unit must leave the interface up", trimmed)
		}
	}
}

// Olric, the SFU and TURN bind the WireGuard address directly, and rqlite and
// the gateway talk to peers across it. On a cold boot they can otherwise start
// before wg0 exists and fail-loop until something restarts them.
func TestWGBindingUnitsAreOrderedAfterTheMesh(t *testing.T) {
	const dep = "orama-namespace-wireguard@index.service"

	units := []string{
		"orama-namespace-rqlite@.service",
		"orama-namespace-olric@.service",
		"orama-namespace-gateway@.service",
		"orama-namespace-pubsub@.service",
		"orama-namespace-sfu@.service",
		"orama-namespace-turn@.service",
		"orama-namespace-ipfs@.service",
		"orama-namespace-vault@.service",
	}

	for _, name := range units {
		t.Run(name, func(t *testing.T) {
			unit := readUnit(t, name)
			var after string
			for _, line := range strings.Split(unit, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "After=") {
					after += line
				}
			}
			if after == "" {
				t.Fatalf("%s declares no After=", name)
			}
			// ipfs@ and vault@ order on the templated instance rather than the
			// index one; either expresses the same dependency.
			if !strings.Contains(after, dep) && !strings.Contains(after, "orama-namespace-wireguard@%i.service") {
				t.Errorf("%s is not ordered after the WireGuard mesh:\n  %s", name, after)
			}
		})
	}
}

// The templates must all be installed, or a unit ordered after the mesh refers
// to something systemd has never seen.
func TestWireGuardTemplateIsInstalled(t *testing.T) {
	found := false
	for _, u := range UnitFilesToInstall() {
		if u == "orama-namespace-wireguard@.service" {
			found = true
		}
	}
	if !found {
		t.Error("the WireGuard template is not in the installed unit set")
	}
}
