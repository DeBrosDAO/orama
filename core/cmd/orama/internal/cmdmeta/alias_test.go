package cmdmeta

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func newTarget() (*cobra.Command, *string, *int) {
	var name string
	var runs int
	cmd := &cobra.Command{
		Use: "archive", Short: "short", Long: "long", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { runs++; return nil },
	}
	cmd.Flags().StringVar(&name, "home", "", "the home")
	MarkNodeLocal(cmd)
	return cmd, &name, &runs
}

func TestHiddenAlias_runsTheTargetWithItsFlags(t *testing.T) {
	target, home, runs := newTarget()
	alias := HiddenAlias(target)

	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}
	old := &cobra.Command{Use: "old"}
	old.AddCommand(alias)
	root.AddCommand(old)
	root.SetArgs([]string{"old", "archive", "--home", "/h"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if *runs != 1 || *home != "/h" {
		t.Errorf("runs = %d, home = %q; the alias must run the target's handler with its flag", *runs, *home)
	}
}

func TestHiddenAlias_isHiddenNodeLocalAndItsOwnCommand(t *testing.T) {
	target, _, _ := newTarget()
	alias := HiddenAlias(target)
	if alias == target {
		t.Fatal("an alias must be a command of its own: cobra keeps one parent per command")
	}
	if !alias.Hidden || target.Hidden {
		t.Error("only the alias is hidden")
	}
	if !IsNodeLocal(alias) {
		t.Error("the alias lost the node-local annotation")
	}
	alias.Annotations["extra"] = "x"
	if _, leaked := target.Annotations["extra"]; leaked {
		t.Error("the alias shares its annotations map with the target")
	}
	if alias.Use != target.Use || alias.Short != target.Short || alias.Long != target.Long {
		t.Error("the alias does not describe itself as the target does")
	}
}

func TestHiddenAlias_keepsTheTargetsArgumentRules(t *testing.T) {
	target, _, _ := newTarget()
	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(HiddenAlias(target))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"archive", "stray"})
	if err := root.Execute(); err == nil {
		t.Fatal("the alias accepted an argument the target refuses")
	}
}

func TestMarkListed_roundTrip(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	if IsListed(cmd) {
		t.Fatal("a fresh command is listed")
	}
	if !IsListed(MarkListed(cmd)) {
		t.Fatal("MarkListed did not mark")
	}
}
