package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// targetCommands is the command set `orama --help` ends with once setup, upgrade,
// edit and remove (built by another batch) and login and logout exist: an
// operator's few commands and the developer commands. A visible top-level
// command outside it is a regression of the CLI reduction.
var targetCommands = map[string]bool{
	"setup": true, "status": true, "upgrade": true, "edit": true, "remove": true, "ssh": true,
	"login": true, "logout": true, "network": true,
	"deploy": true, "app": true, "db": true, "function": true, "domain": true, "namespace": true,
	"members": true, "storage": true, "audit": true,
	"version": true,
	// auth stays visible for `auth login`, `logout` and `whoami` until login and
	// logout stand on their own at the top level.
	"auth": true,
}

// existingVisible are the target commands that exist today; the others are built
// by other work.
var existingVisible = []string{
	"setup", "status", "upgrade", "edit", "remove", "ssh", "network", "deploy", "app", "db", "function", "domain", "namespace",
	"members", "storage", "audit", "version", "auth",
}

// maintCommands are the commands that moved under `orama maint`, with a hidden
// group of the same name nowhere else visible.
var maintCommands = []string{
	"build", "push", "rollout", "inspect", "sandbox", "invite", "operator", "cluster", "vpn",
	"node", "global", "network", "release",
}

func visibleTopLevel(root *cobra.Command) []string {
	var names []string
	for _, c := range root.Commands() {
		if !c.Hidden && c.Name() != "help" && c.Name() != "completion" {
			names = append(names, c.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestVisibleTopLevelCommands_areWithinTheTargetSet(t *testing.T) {
	for _, name := range visibleTopLevel(newRootCmd()) {
		if !targetCommands[name] {
			t.Errorf("`orama %s` is visible in `orama --help` but is not in the target set; move it under `orama maint` or hide it", name)
		}
	}
}

func TestVisibleTopLevelCommands_theExistingTargetCommandsStayVisible(t *testing.T) {
	visible := strings.Join(visibleTopLevel(newRootCmd()), " ")
	for _, name := range existingVisible {
		if !strings.Contains(" "+visible+" ", " "+name+" ") {
			t.Errorf("`orama %s` is not visible (visible: %s)", name, visible)
		}
	}
}

func TestMaintCommands_areNotVisibleAtTheRoot(t *testing.T) {
	root := newRootCmd()
	maint := findCommand(t, root, []string{"maint"})
	if !maint.Hidden {
		t.Fatal("`orama maint` must be hidden from `orama --help`")
	}
	visible := map[string]bool{}
	for _, name := range visibleTopLevel(root) {
		visible[name] = true
	}
	var inMaint []string
	for _, c := range maint.Commands() {
		inMaint = append(inMaint, c.Name())
		// `orama maint network` (publish) is not `orama network`.
		if visible[c.Name()] && c.Name() != "network" {
			t.Errorf("`orama %s` moved under `orama maint` and is still visible at the root", c.Name())
		}
	}
	sort.Strings(inMaint)
	want := append([]string(nil), maintCommands...)
	sort.Strings(want)
	if strings.Join(inMaint, " ") != strings.Join(want, " ") {
		t.Errorf("orama maint holds %v, want %v", inMaint, want)
	}
}

// moved maps each command that left its old place to where it lives now. A copy
// left at the old path would be a second definition, which is what the reduction
// removes; the paths a unit runs are the exception, kept as hidden aliases and
// tested in node_local_units_test.go.
var moved = map[string]string{
	"build": "maint build", "push": "maint push", "rollout": "maint rollout", "inspect": "maint inspect",
	"sandbox": "maint sandbox", "invite": "maint invite", "operator": "maint operator", "vpn": "maint vpn",
	"node push": "maint push", "node rollout": "maint rollout",
	"node install": "maint node install", "node stage-archive": "maint node stage-archive",
	"node recover-raft": "maint node recover-raft", "node migrate-conf": "maint node migrate-conf",
	"node migrate-raft-id": "maint node migrate-raft-id", "node schema": "maint node schema",
	"node enroll": "maint node enroll", "node unlock": "maint node unlock",
	"node autoupdate":  "maint node autoupdate",
	"global validator": "maint global validator", "global stage-oramad": "maint global stage-oramad",
	"global tor ceremony": "maint global tor ceremony", "global tor onions": "maint global tor onions",
	"cluster settings": "maint cluster settings", "cluster creators": "maint cluster creators",
}

// resolves reports whether `orama <path>` names a command of exactly that path.
func resolves(root *cobra.Command, path string) bool {
	words := strings.Fields(path)
	cmd, rest, err := root.Find(words)
	return err == nil && len(rest) == 0 && cmd != root && cmd.Name() == words[len(words)-1]
}

func TestMovedCommands_existOnlyUnderMaint(t *testing.T) {
	root := newRootCmd()
	for oldPath, newPath := range moved {
		if cmd, _, _ := root.Find(strings.Fields(oldPath)); resolves(root, oldPath) && !cmd.Hidden {
			// A hidden command at the old path is the alias a unit or a remote
			// caller still runs; only a visible one is a second definition.
			t.Errorf("`orama %s` still exists; it moved to `orama %s`", oldPath, newPath)
		}
		if !resolves(root, newPath) {
			t.Errorf("`orama %s` does not exist", newPath)
		}
	}
}

// The removals of the audit: deprecated spellings and flags are gone, not hidden.
func TestRemovedCommandsAndFlags_areGone(t *testing.T) {
	root := newRootCmd()
	for _, path := range []string{"node clean", "global status", "node decommission"} {
		if resolves(root, path) {
			t.Errorf("`orama %s` still exists", path)
		}
	}
	remove := findCommand(t, root, []string{"node", "remove"})
	if len(remove.Aliases) != 0 {
		t.Errorf("node remove still has aliases %v", remove.Aliases)
	}
	for _, c := range []struct {
		path []string
		flag string
	}{
		{[]string{"maint", "build"}, "sign"},
		{[]string{"maint", "push"}, "ip"},
		{[]string{"maint", "push"}, "fanout"},
		{[]string{"maint", "node", "install"}, "cluster-secret"},
		{[]string{"maint", "node", "install"}, "swarm-key"},
	} {
		cmd := findCommand(t, root, c.path)
		if cmd.Flags().Lookup(c.flag) != nil {
			t.Errorf("`orama %s` still takes --%s", strings.Join(c.path, " "), c.flag)
		}
	}
}
