package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/cmdmeta"
)

// unitCommandRE finds the orama command a unit runs: in a unit file
// (ExecStart=/opt/orama/bin/orama <words>) or in a Go unit renderer, where the
// binary is a format verb ("%s/%s global tor monitor --home %s").
var unitCommandRE = regexp.MustCompile(`(?:/bin/orama|%s/%s) ((?:[a-z][a-z-]* ?)+)`)

// unitCommandsWithoutEnvironment are run by units but are not node-local
// commands of the CLI: serve-ipfs-cluster is its own process and is already
// exempt in needsEnvironmentCAs.
var unitCommandsWithoutEnvironment = map[string]bool{"serve-ipfs-cluster": true}

// A command a systemd unit runs has no home directory and no operator
// environment. orama-autoupdate.service failed on every node with "$HOME is
// not defined" because `node autoupdate run` was not declared node-local and
// the root command loaded the operator's environment CAs for it.
func TestUnitCommands_areNodeLocal(t *testing.T) {
	root := newRootCmd()
	sources := append(globOrFail(t, "../../systemd/*.service"), globOrFail(t, "../../pkg/install/*.go")...)
	found := 0
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range unitCommandRE.FindAllStringSubmatch(string(body), -1) {
			args := strings.Fields(m[1])
			if unitCommandsWithoutEnvironment[args[0]] {
				continue
			}
			cmd, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 || cmd == root {
				continue // not a command of this CLI (e.g. the orama-global binary)
			}
			found++
			if !cmdmeta.IsNodeLocal(cmd) || needsEnvironmentCAs(cmd) {
				t.Errorf("%s runs `orama %s` from a unit, but the command is not node-local", filepath.Base(path), cmd.CommandPath())
			}
		}
	}
	if found == 0 {
		t.Fatal("found no orama command in any unit; the scan is broken")
	}
}

func TestNeedsEnvironmentCAs_nodeLocalCommandsNeedNone(t *testing.T) {
	root := newRootCmd()
	for _, args := range []string{"node autoupdate run", "global tor monitor", "global tor archive", "global txgate"} {
		cmd, _, err := root.Find(strings.Fields(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if needsEnvironmentCAs(cmd) {
			t.Errorf("needsEnvironmentCAs(%q) = true; a unit runs it with no home", args)
		}
	}
	upgrade, _, err := root.Find([]string{"node", "upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	if cmdmeta.IsNodeLocal(upgrade) {
		t.Error("node upgrade is run by an operator too; it must not be marked node-local")
	}
}

func globOrFail(t *testing.T, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil || len(m) == 0 {
		t.Fatalf("glob %s: %v (%d files)", pattern, err, len(m))
	}
	return m
}
