package upgrade

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// decodeUpgrade returns the script upgradeCommand pipes to bash.
func decodeUpgrade(t *testing.T, cmd string) string {
	t.Helper()
	fields := strings.Fields(cmd)
	if len(fields) < 3 || fields[0] != "printf" {
		t.Fatalf("unexpected command shape: %s", cmd)
	}
	script, err := base64.StdEncoding.DecodeString(fields[2])
	if err != nil {
		t.Fatal(err)
	}
	return string(script)
}

// The rolling upgrade runs the staged build's CLI, so the whole upgrade —
// the hand-over and the stop included — is the new release's code.
func TestUpgradeCommand_runsTheStagedCLI(t *testing.T) {
	no := false
	for _, tc := range []struct {
		sudo  string
		flags Flags
		want  string
	}{
		{"sudo ", Flags{}, "exec /opt/orama/bin/orama node upgrade --restart\n"},
		{"", Flags{Force: true, SkipChecks: true}, "exec /opt/orama/bin/orama node upgrade --restart --force --skip-checks\n"},
		{"sudo ", Flags{Nameserver: &no}, "exec /opt/orama/bin/orama node upgrade --restart --nameserver=false\n"},
	} {
		cmd := upgradeCommand(tc.sudo, &tc.flags)
		if !strings.HasSuffix(cmd, "| "+tc.sudo+"bash -s") {
			t.Errorf("not run through %sbash: %s", tc.sudo, cmd)
		}
		if script := decodeUpgrade(t, cmd); !strings.HasSuffix(script, tc.want) {
			t.Errorf("script ends %q, want %q", script[strings.LastIndex(script, "exec"):], tc.want)
		}
	}
}

// fakeStaged is a staged tree in a temp dir, owned by the test's user.
func fakeStaged(t *testing.T, anchor bool) stagedPaths {
	t.Helper()
	dir := t.TempDir()
	p := stagedPaths{base: dir, bin: filepath.Join(dir, "bin"), cli: filepath.Join(dir, "bin", "orama"), anchor: filepath.Join(dir, "archive-signers")}
	if err := os.MkdirAll(p.bin, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.cli, []byte("#!/bin/sh\necho RAN\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	if anchor {
		if err := os.WriteFile(p.anchor, []byte("0xabc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func runGuard(t *testing.T, p stagedPaths) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(upgradeScript(p, "node upgrade --restart"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A node that was never pushed a signed build has no anchor, and its
// /opt/orama/bin/orama is its old release's: it is not run.
func TestUpgradeScript_refusesANodeWithoutAnAnchor(t *testing.T) {
	out, err := runGuard(t, fakeStaged(t, false))
	if err == nil || strings.Contains(out, "RAN") {
		t.Fatalf("ran without an anchor: %q", out)
	}
	if !strings.Contains(out, "orama push") {
		t.Errorf("the refusal does not say to push: %q", out)
	}
}

// A staged tree anyone but root owns is not run as root.
func TestUpgradeScript_refusesATreeRootDoesNotOwn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the temp tree is root's")
	}
	out, err := runGuard(t, fakeStaged(t, true))
	if err == nil || strings.Contains(out, "RAN") {
		t.Fatalf("ran a CLI root does not own: %q", out)
	}
	if !strings.Contains(out, "not root's alone") {
		t.Errorf("unexpected refusal: %q", out)
	}
}

// A symlinked CLI is refused before its ownership is looked at.
func TestUpgradeScript_refusesASymlinkedCLI(t *testing.T) {
	p := fakeStaged(t, true)
	real := filepath.Join(t.TempDir(), "orama")
	if err := os.Rename(p.cli, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, p.cli); err != nil {
		t.Fatal(err)
	}
	p.base, p.bin = p.cli, p.cli // check the symlink first
	out, err := runGuard(t, p)
	if err == nil || !strings.Contains(out, "is a symlink") {
		t.Fatalf("a symlinked CLI was not refused: %q, %v", out, err)
	}
}

func TestUpgradeScript_isValidShell(t *testing.T) {
	script := upgradeScript(nodeStagedPaths, "node upgrade --restart")
	if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("not valid shell: %v\n%s\n%s", err, out, script)
	}
}
