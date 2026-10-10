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

// unitCommands are the orama commands the installed units run, with the path
// they ran before `orama maint` existed. A unit already installed on a node keeps
// its ExecStart until an upgrade rewrites it, so each of these must still resolve
// to a command, the hidden alias of the one that moved.
var unitCommands = []string{
	"node autoupdate run",
	"node ipfs-gc",
	"global validator check-sign-floor",
	"global tor archive",
	"global tor monitor",
	"global txgate",
	"serve-ipfs-cluster",
}

// Every command a unit runs is written in the unit file or in the installer's
// renderer, and resolves in the command tree at that path. If the installer
// starts running another path, this fails until the path is listed here.
func TestUnitCommands_everyExecStartResolves(t *testing.T) {
	root := newRootCmd()
	sources := append(globOrFail(t, "../../systemd/*.service"), globOrFail(t, "../../pkg/install/*.go")...)
	var corpus strings.Builder
	for _, path := range sources {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		corpus.Write(body)
	}
	for _, line := range unitCommands {
		args := strings.Fields(line)
		if !strings.Contains(corpus.String(), strings.Join(args, " ")) {
			t.Errorf("no unit runs `orama %s` any more; drop it from unitCommands (and its alias if nothing needs it)", line)
		}
		cmd, rest, err := root.Find(args)
		if err != nil || len(rest) > 0 || cmd == root || !cmd.Runnable() {
			t.Errorf("a unit runs `orama %s`, which is not a runnable command (rest %v, err %v)", line, rest, err)
		}
	}
}

// A path that moved keeps a hidden alias for the installed unit. The alias is
// hidden, node-local like its target, and not the canonical command: that one
// lives under `orama maint`.
func TestUnitCommands_movedPathsAreHiddenAliasesOfTheMaintCommand(t *testing.T) {
	root := newRootCmd()
	for oldPath, canonical := range map[string]string{
		"node autoupdate run":               "maint node autoupdate run",
		"global validator check-sign-floor": "maint global validator check-sign-floor",
		"global tor archive":                "maint global tor archive",
		"global tor monitor":                "maint global tor monitor",
		"global txgate":                     "maint global txgate",
	} {
		alias, _, err := root.Find(strings.Fields(oldPath))
		if err != nil {
			t.Fatalf("%s: %v", oldPath, err)
		}
		target, _, err := root.Find(strings.Fields(canonical))
		if err != nil {
			t.Fatalf("%s: %v", canonical, err)
		}
		if alias == target || !alias.Hidden || target.Hidden {
			t.Errorf("%s: want a hidden alias of a visible-in-maint command (alias hidden %v, same %v, target hidden %v)",
				oldPath, alias.Hidden, alias == target, target.Hidden)
		}
		if cmdmeta.IsNodeLocal(target) != cmdmeta.IsNodeLocal(alias) || !cmdmeta.IsNodeLocal(alias) {
			t.Errorf("%s and %s must both be node-local: a unit runs it with no home", oldPath, canonical)
		}
		if alias.Short == "" || alias.RunE == nil && alias.Run == nil {
			t.Errorf("%s has no handler", oldPath)
		}
	}
}

func TestNeedsEnvironmentCAs_nodeLocalCommandsNeedNone(t *testing.T) {
	root := newRootCmd()
	for _, args := range []string{
		"node autoupdate run", "global tor monitor", "global tor archive", "global txgate",
		"maint node autoupdate run", "maint global tor monitor", "maint global tor archive", "maint global txgate",
	} {
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

// The installed-path aliases take exactly the flags of the command that moved:
// a unit or a remote caller passes the flags it always passed.
func TestHiddenAliases_takeTheFlagsOfTheCommandThatMoved(t *testing.T) {
	root := newRootCmd()
	for oldPath, canonical := range map[string]string{
		"node install":        "maint node install",
		"node stage-archive":  "maint node stage-archive",
		"node autoupdate run": "maint node autoupdate run",
		"global tor archive":  "maint global tor archive",
		"global tor monitor":  "maint global tor monitor",
		"global txgate":       "maint global txgate",
	} {
		alias := findCommand(t, root, strings.Fields(oldPath))
		target := findCommand(t, root, strings.Fields(canonical))
		if !alias.Hidden {
			t.Errorf("orama %s must be hidden", oldPath)
		}
		if got, want := strings.Join(flagSpec(alias), "\n"), strings.Join(flagSpec(target), "\n"); got != want {
			t.Errorf("orama %s and orama %s take different flags.\nalias:\n%s\ntarget:\n%s", oldPath, canonical, got, want)
		}
		if len(flagSpec(target)) == 0 && oldPath != "node autoupdate run" {
			t.Errorf("orama %s has no flags; the comparison proves nothing", canonical)
		}
	}
}
