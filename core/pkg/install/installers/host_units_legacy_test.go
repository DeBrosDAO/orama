package installers

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostUnitSystem answers the commands LegacyHostUnitCleaner runs from a model
// of a node's unit directory, and records them.
type hostUnitSystem struct {
	unitDir  string
	states   map[string]string // unit -> LoadState; absent means not-found
	failStop string            // unit whose stop fails
	calls    []string
}

func (h *hostUnitSystem) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	h.calls = append(h.calls, call)
	if name != "systemctl" {
		return "", errors.New("unexpected command: " + call)
	}
	unit := args[len(args)-1]
	switch args[0] {
	case "show":
		if state, ok := h.states[unit]; ok {
			return state + "\n", nil
		}
		return "not-found\n", nil
	case "unmask":
		h.states[unit] = "loaded"
		return "", os.Remove(filepath.Join(h.unitDir, unit))
	case "stop":
		if unit == h.failStop {
			return "Job for " + unit + " failed", errors.New("exit status 1")
		}
		return "", nil
	case "disable":
		return "", nil
	}
	return "", errors.New("unexpected command: " + call)
}

func newHostUnitCleaner(t *testing.T) (*LegacyHostUnitCleaner, *hostUnitSystem) {
	t.Helper()
	root := t.TempDir()
	unitDir := filepath.Join(root, hostUnitDir)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sys := &hostUnitSystem{unitDir: unitDir, states: map[string]string{}}
	return &LegacyHostUnitCleaner{run: sys.run, root: root, logWriter: io.Discard}, sys
}

func writeUnitFile(t *testing.T, sys *hostUnitSystem, unit string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(sys.unitDir, unit), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *hostUnitSystem) called(call string) bool {
	for _, c := range h.calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestLegacyHostUnitCleaner_removesEveryLegacyUnit(t *testing.T) {
	c, sys := newHostUnitCleaner(t)
	for _, unit := range LegacyHostUnits {
		writeUnitFile(t, sys, unit)
		sys.states[unit] = "loaded"
	}
	// orama-node's own unit sits beside them and must survive.
	writeUnitFile(t, sys, "orama-node.service")

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	for _, unit := range LegacyHostUnits {
		if _, err := os.Lstat(filepath.Join(sys.unitDir, unit)); !os.IsNotExist(err) {
			t.Errorf("%s is still on disk (err=%v)", unit, err)
		}
		for _, want := range []string{"systemctl stop " + unit, "systemctl disable " + unit} {
			if !sys.called(want) {
				t.Errorf("%q never ran", want)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(sys.unitDir, "orama-node.service")); err != nil {
		t.Errorf("orama-node.service was touched: %v", err)
	}
}

// A node installed after the change, or cleaned already, has none of the
// files: nothing is stopped or disabled — a package's own unit of the same
// name under /lib/systemd is not Orama's to touch.
func TestLegacyHostUnitCleaner_nodeWithoutLegacyUnitsIsANoop(t *testing.T) {
	c, sys := newHostUnitCleaner(t)
	sys.states["caddy.service"] = "loaded" // e.g. a distro unit elsewhere

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if len(sys.calls) != 0 {
		t.Errorf("commands ran on a node with no legacy unit files: %v", sys.calls)
	}
}

// `orama node stop` masks what it stops; disable refuses a masked unit.
func TestLegacyHostUnitCleaner_unmasksBeforeRemoving(t *testing.T) {
	c, sys := newHostUnitCleaner(t)
	if err := os.Symlink("/dev/null", filepath.Join(sys.unitDir, "orama-olric.service")); err != nil {
		t.Fatal(err)
	}
	sys.states["orama-olric.service"] = "masked"

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if !sys.called("systemctl unmask orama-olric.service") {
		t.Errorf("masked unit was not unmasked: %v", sys.calls)
	}
	if sys.called("systemctl disable orama-olric.service") {
		t.Errorf("disable ran on a unit whose only file was its mask: %v", sys.calls)
	}
}

// A unit systemd has not loaded is not running, but its enablement links
// still go.
func TestLegacyHostUnitCleaner_unloadedUnitIsDisabledNotStopped(t *testing.T) {
	c, sys := newHostUnitCleaner(t)
	writeUnitFile(t, sys, "coredns.service")

	if err := c.Remove(); err != nil {
		t.Fatal(err)
	}
	if sys.called("systemctl stop coredns.service") {
		t.Error("stopped a unit systemd never loaded")
	}
	if !sys.called("systemctl disable coredns.service") {
		t.Error("an unloaded unit's enablement links were left behind")
	}
	if _, err := os.Lstat(filepath.Join(sys.unitDir, "coredns.service")); !os.IsNotExist(err) {
		t.Errorf("coredns.service is still on disk (err=%v)", err)
	}
}

// A unit that will not stop keeps its file: deleting it would leave a running
// daemon systemd can no longer describe, racing @index for its port.
func TestLegacyHostUnitCleaner_failedStopIsFatalAndKeepsTheFile(t *testing.T) {
	c, sys := newHostUnitCleaner(t)
	writeUnitFile(t, sys, "orama-ipfs.service")
	sys.states["orama-ipfs.service"] = "loaded"
	sys.failStop = "orama-ipfs.service"

	err := c.Remove()
	if err == nil || !strings.Contains(err.Error(), "orama-ipfs.service") {
		t.Fatalf("Remove() = %v, want an error naming the unit", err)
	}
	if _, statErr := os.Stat(filepath.Join(sys.unitDir, "orama-ipfs.service")); statErr != nil {
		t.Errorf("unit file deleted after its stop failed: %v", statErr)
	}
}

// Every name is a bare unit file name: the cleaner joins it to the unit
// directory and deletes the result, so a separator would reach elsewhere.
func TestLegacyHostUnits_areBareUnitNames(t *testing.T) {
	for _, unit := range LegacyHostUnits {
		if unit != filepath.Base(unit) || strings.Contains(unit, "..") {
			t.Errorf("%q is not a bare unit file name", unit)
		}
		if !strings.HasSuffix(unit, ".service") && !strings.HasSuffix(unit, ".timer") {
			t.Errorf("%q is not a unit file name", unit)
		}
		if strings.HasPrefix(unit, "orama-node") || strings.HasPrefix(unit, "orama-namespace-") || strings.HasPrefix(unit, "orama-privhelper") {
			t.Errorf("%q is a unit install still writes", unit)
		}
	}
}
