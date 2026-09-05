package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// `orama push` and `orama node push` were two separate implementations with
// opposite defaults for whether the archive fans out, and two fanouts with
// different key handling. `orama rollout` and `orama node rollout` likewise:
// one built first, the other did not, and only one of them tried to restart the
// raft leader last. Which behaviour you got depended on which of two identically
// named commands you happened to type.
//
// They are now one definition mounted twice. These pin that down: a command
// reachable under two names must accept the same flags with the same defaults
// and describe itself the same way.

// findCommand walks to the command named by path, e.g. {"node", "push"}.
func findCommand(t *testing.T, root *cobra.Command, path []string) *cobra.Command {
	t.Helper()
	cur := root
	for _, name := range path {
		var next *cobra.Command
		for _, sub := range cur.Commands() {
			if sub.Name() == name {
				next = sub
				break
			}
		}
		if next == nil {
			t.Fatalf("command %q not found under %q", name, cur.Name())
		}
		cur = next
	}
	return cur
}

// flagSpec is every flag a command declares itself, as "name=default".
//
// Not cmd.Flags(): running a command merges its inherited flags into its own
// set and leaves them there, and cobra injects --help on first run, so the
// answer depends on what else ran first in the process. The two spellings sit
// under different parents — root and node — so their inherited flags differ
// legitimately; only the flags they declare themselves have to match. ownFlags
// (reference_test.go) computes exactly that, the same way for both.
func flagSpec(cmd *cobra.Command) []string {
	var out []string
	for _, f := range ownFlags(cmd) {
		out = append(out, f.Name+"="+f.DefValue)
	}
	sort.Strings(out)
	return out
}

func TestAliasedCommandsShareOneDefinition(t *testing.T) {
	for _, pair := range []struct {
		top    []string
		nested []string
	}{
		{[]string{"push"}, []string{"node", "push"}},
		{[]string{"rollout"}, []string{"node", "rollout"}},
		{[]string{"nodes"}, []string{"node", "list"}},
	} {
		name := strings.Join(pair.top, " ") + " vs " + strings.Join(pair.nested, " ")
		t.Run(name, func(t *testing.T) {
			root := newRootCmd()
			top := findCommand(t, root, pair.top)
			nested := findCommand(t, root, pair.nested)

			topFlags := strings.Join(flagSpec(top), "\n")
			nestedFlags := strings.Join(flagSpec(nested), "\n")
			if topFlags != nestedFlags {
				t.Errorf("flags differ.\norama %s:\n%s\n\norama %s:\n%s",
					strings.Join(pair.top, " "), topFlags,
					strings.Join(pair.nested, " "), nestedFlags)
			}

			if top.Short != nested.Short {
				t.Errorf("Short differs: %q vs %q", top.Short, nested.Short)
			}
			if top.Long != nested.Long {
				t.Errorf("Long differs; the two spellings must describe the same behaviour")
			}
		})
	}
}

// The two spellings must be distinct command objects: cobra stores a single
// parent per command, so mounting one object twice silently breaks help paths.
func TestAliasedCommandsAreDistinctObjects(t *testing.T) {
	root := newRootCmd()
	top := findCommand(t, root, []string{"push"})
	nested := findCommand(t, root, []string{"node", "push"})
	if top == nested {
		t.Fatal("the same *cobra.Command is mounted under two parents")
	}
	if nested.Parent().Name() != "node" {
		t.Errorf("orama node push has parent %q, want node", nested.Parent().Name())
	}
}

// `orama env enable` was printed in help and handled by the dispatcher but
// never registered, so it could not be run.
func TestEnvEnableIsReachable(t *testing.T) {
	root := newRootCmd()
	env := findCommand(t, root, []string{"env"})
	target, _, err := env.Find([]string{"enable"})
	if err != nil {
		t.Fatalf("orama env enable must resolve: %v", err)
	}
	if target.Name() != "use" {
		t.Errorf("orama env enable resolved to %q, want use", target.Name())
	}
}
