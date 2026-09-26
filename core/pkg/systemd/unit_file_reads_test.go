package systemd

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// systemd reads EnvironmentFile= and LoadCredential= as PID 1 and follows
// symlinks. A template naming a path the orama user can write lets a
// compromised orama process symlink it to any root-only file (the WireGuard
// private key) and read it back through the unit. Such files must live in the
// root-owned trees (pkg/unitenv, pkg/deploysecrets).
//
// StandardInput=/StandardOutput=/StandardError= with file:, append: or
// truncate: are the same hazard in the other direction: PID 1 opens (and
// creates) the path before switching to User=, with a plain open(2)
// (src/core/exec-invoke.c, setup_output → acquire_path), so an orama-owned log
// file symlinked to a root-owned one has root write the service's output
// into it. Units log to the journal. The Go-generated units are checked by
// pkg/install's TestGoGeneratedUnits_PID1NeverOpensAnOramaOwnedPath.
func TestUnitTemplates_PID1NeverReadsAnOramaOwnedPath(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "systemd")
	units, err := filepath.Glob(filepath.Join(dir, "*.service"))
	if err != nil || len(units) == 0 {
		t.Fatalf("no unit templates found in %s: %v", dir, err)
	}
	reads := regexp.MustCompile(`(?m)^(EnvironmentFile|LoadCredential)=(.*)$`)
	stdio := regexp.MustCompile(`(?m)^(StandardInput|StandardOutput|StandardError)=(file|append|truncate):(.*)$`)
	for _, u := range units {
		data, err := os.ReadFile(u)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range reads.FindAllStringSubmatch(string(data), -1) {
			if strings.Contains(m[2], "/opt/orama") {
				t.Errorf("%s: %s=%s reads an orama-owned path as PID 1", filepath.Base(u), m[1], m[2])
			}
		}
		for _, m := range stdio.FindAllStringSubmatch(string(data), -1) {
			if strings.Contains(m[3], "/opt/orama") {
				t.Errorf("%s: %s=%s:%s opens an orama-owned path as PID 1", filepath.Base(u), m[1], m[2], m[3])
			}
		}
	}
}
