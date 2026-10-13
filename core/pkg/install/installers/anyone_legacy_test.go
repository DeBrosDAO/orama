package installers

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystem answers the commands LegacyAnyoneCleaner runs from a model of a
// node, and records every command it was asked to run.
type fakeSystem struct {
	units        map[string]string // unit -> LoadState; absent means not-found
	packages     map[string]string // package -> dpkg status; absent means unknown to dpkg
	failStop     bool
	calls        []string
	destructive  []string
	daemonReload int
}

func (f *fakeSystem) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case name == "systemctl" && args[0] == "show":
		if state, ok := f.units[args[len(args)-1]]; ok {
			return state + "\n", nil
		}
		return "not-found\n", nil
	case name == "systemctl" && args[0] == "unmask":
		f.destructive = append(f.destructive, call)
		f.units[args[1]] = "loaded"
		return "", nil
	case name == "systemctl" && args[0] == "disable" && f.units[args[1]] == "masked":
		return "Failed to disable unit: Unit file " + args[1] + " is masked.", errors.New("exit status 1")
	case name == "systemctl" && args[0] == "stop":
		f.destructive = append(f.destructive, call)
		if f.failStop {
			return "Job failed", errors.New("exit status 1")
		}
		return "", nil
	case name == "systemctl" && args[0] == "disable":
		f.destructive = append(f.destructive, call)
		delete(f.units, args[1])
		return "", nil
	case name == "systemctl" && args[0] == "daemon-reload":
		f.daemonReload++
		return "", nil
	case name == "dpkg-query":
		pkg := args[len(args)-1]
		status, ok := f.packages[pkg]
		if !ok {
			return "dpkg-query: no packages found matching " + pkg + "\n", errors.New("exit status 1")
		}
		return status, nil
	case name == "apt-get" && containsString(args, "purge"):
		f.destructive = append(f.destructive, call)
		f.packages[args[len(args)-1]] = "not-installed"
		return "", nil
	}
	return "", errors.New("unexpected command: " + call)
}

func newTestCleaner(t *testing.T, sys *fakeSystem) (*LegacyAnyoneCleaner, string) {
	t.Helper()
	root := t.TempDir()
	return &LegacyAnyoneCleaner{run: sys.run, root: root, oramaDir: "/opt/orama/.orama", logWriter: io.Discard}, root
}

// seedAnyoneFootprint creates every legacy path under root.
func seedAnyoneFootprint(t *testing.T, root, oramaDir string) {
	t.Helper()
	for _, p := range LegacyAnyonePaths(oramaDir) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func anyoneNode() *fakeSystem {
	units := map[string]string{}
	for _, u := range LegacyAnyoneUnits {
		units[u] = "loaded"
	}
	return &fakeSystem{units: units, packages: map[string]string{"anon": "installed", "nyx": "installed"}}
}

func cleanNode() *fakeSystem {
	return &fakeSystem{units: map[string]string{}, packages: map[string]string{}}
}

func TestLegacyAnyoneCleaner_Remove_removesEverything(t *testing.T) {
	sys := anyoneNode()
	c, root := newTestCleaner(t, sys)
	seedAnyoneFootprint(t, root, c.oramaDir)

	if err := c.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, u := range LegacyAnyoneUnits {
		for _, verb := range []string{"stop", "disable"} {
			want := "systemctl " + verb + " " + u
			if !containsString(sys.destructive, want) {
				t.Errorf("missing %q", want)
			}
		}
	}
	for _, pkg := range []string{"anon", "nyx"} {
		if !containsString(sys.destructive, "apt-get -o DPkg::Lock::Timeout=300 purge -y "+pkg) {
			t.Errorf("the %s package must be purged", pkg)
		}
	}
	for _, p := range LegacyAnyonePaths(c.oramaDir) {
		if _, err := os.Lstat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}
	if sys.daemonReload != 1 {
		t.Errorf("daemon-reload ran %d times, want 1", sys.daemonReload)
	}
}

// The first thing stopped must be the instance that holds the SOCKS port.
func TestLegacyAnyoneCleaner_Remove_stopsTheRunningInstanceFirst(t *testing.T) {
	sys := anyoneNode()
	c, _ := newTestCleaner(t, sys)
	if err := c.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(sys.destructive) == 0 || sys.destructive[0] != "systemctl stop orama-namespace-anyone-client@index.service" {
		t.Errorf("first destructive command = %v, want the @index instance stopped", sys.destructive)
	}
}

func TestLegacyAnyoneCleaner_Remove_isIdempotent(t *testing.T) {
	sys := anyoneNode()
	c, root := newTestCleaner(t, sys)
	seedAnyoneFootprint(t, root, c.oramaDir)
	if err := c.Remove(); err != nil {
		t.Fatalf("first Remove: %v", err)
	}
	sys.destructive = nil
	if err := c.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if len(sys.destructive) != 0 {
		t.Errorf("second run acted again: %v", sys.destructive)
	}
}

func TestLegacyAnyoneCleaner_Remove_neverHadAnyone(t *testing.T) {
	sys := cleanNode()
	c, _ := newTestCleaner(t, sys)
	if err := c.Remove(); err != nil {
		t.Fatalf("Remove on a clean node: %v", err)
	}
	if len(sys.destructive) != 0 {
		t.Errorf("a node without Anyone must be left alone, ran: %v", sys.destructive)
	}
}

// A package left in config-files state still owns /etc/anon; purge it too.
func TestLegacyAnyoneCleaner_Remove_purgesConfigFilesState(t *testing.T) {
	sys := cleanNode()
	sys.packages["anon"] = "config-files"
	c, _ := newTestCleaner(t, sys)
	if err := c.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !containsString(sys.destructive, "apt-get -o DPkg::Lock::Timeout=300 purge -y anon") {
		t.Error("a config-files package must be purged")
	}
}

// A unit that will not stop keeps the SOCKS port; carrying on would start Tor
// into a port clash.
func TestLegacyAnyoneCleaner_Remove_stopFailureIsAnError(t *testing.T) {
	sys := anyoneNode()
	sys.failStop = true
	c, _ := newTestCleaner(t, sys)
	err := c.Remove()
	if err == nil {
		t.Fatal("a failed stop must be returned, not logged and skipped")
	}
	if !strings.Contains(err.Error(), "orama-namespace-anyone-client@index.service") {
		t.Errorf("error should name the unit: %v", err)
	}
}

// `orama node stop` masks every service it stops, so a node stopped by an old
// CLI can carry a masked Anyone unit; disable refuses a masked unit.
func TestLegacyAnyoneCleaner_Remove_unmasksAMaskedUnitFirst(t *testing.T) {
	sys := cleanNode()
	sys.units["orama-anyone-relay.service"] = "masked"
	c, _ := newTestCleaner(t, sys)
	if err := c.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	want := []string{
		"systemctl unmask orama-anyone-relay.service",
		"systemctl stop orama-anyone-relay.service",
		"systemctl disable orama-anyone-relay.service",
	}
	if strings.Join(sys.destructive, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands = %v, want %v", sys.destructive, want)
	}
}

// A package that was never installed is left to dpkg's silence, not purged.
func TestLegacyAnyoneCleaner_Remove_onlyPurgesKnownPackages(t *testing.T) {
	sys := cleanNode()
	sys.packages["anon"] = "installed"
	c, _ := newTestCleaner(t, sys)
	if err := c.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if containsString(sys.destructive, "apt-get -o DPkg::Lock::Timeout=300 purge -y nyx") {
		t.Error("nyx is not installed and must not be purged")
	}
	if !containsString(sys.destructive, "apt-get -o DPkg::Lock::Timeout=300 purge -y anon") {
		t.Error("anon must be purged")
	}
}

func TestLegacyAnyonePaths_coverTheAnyoneFootprint(t *testing.T) {
	paths := LegacyAnyonePaths("/opt/orama/.orama")
	for _, want := range []string{
		"/etc/anon", "/var/lib/anon", "/var/log/anon",
		"/etc/apt/sources.list.d/anon.list", "/etc/apt/trusted.gpg.d/anon.asc",
		"/etc/systemd/system/orama-namespace-anyone-client@.service",
		"/opt/orama/.orama/data/namespaces/index/anyone-client.env",
	} {
		if !containsString(paths, want) {
			t.Errorf("LegacyAnyonePaths missing %s", want)
		}
	}
	for _, p := range paths {
		if strings.Contains(p, "tor") && !strings.Contains(p, "systemd") {
			t.Errorf("legacy cleanup must not touch Tor: %s", p)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
