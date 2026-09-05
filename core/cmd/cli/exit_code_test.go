package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/spf13/cobra"
)

// Every CLI failure used to be os.Exit(1) from inside a handler, so a script
// could not tell "you typed the flag wrong" from "the cluster lost quorum".
// Cobra's own argument and flag validation has to carry that classification
// too, or half the mistakes a user makes still come back as the generic code.

// The real command tree is built from package-level command variables, and
// cobra retains parsed state on them across Execute calls. These tests build
// their own tree so one case cannot leave state for the next.
func newTestTree() *cobra.Command {
	root := &cobra.Command{Use: "orama", SilenceUsage: true, SilenceErrors: true}

	group := &cobra.Command{Use: "node", Short: "node group"}
	group.AddCommand(&cobra.Command{
		Use:  "push",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	group.AddCommand(&cobra.Command{
		Use:  "use",
		Args: cobra.ExactArgs(1),
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.AddCommand(group)

	classifyUsageErrors(root)
	return root
}

func execTree(t *testing.T, args ...string) error {
	t.Helper()
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	return root.Execute()
}

func TestUnknownFlagExitsWithTheUsageCode(t *testing.T) {
	err := execTree(t, "node", "push", "--definitely-not-a-flag")
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
	if got := clierr.CodeOf(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want %d for a flag mistake", got, clierr.CodeUsage)
	}
}

func TestWrongArgumentCountExitsWithTheUsageCode(t *testing.T) {
	err := execTree(t, "node", "use")
	if err == nil {
		t.Fatal("a missing argument must be an error")
	}
	if got := clierr.CodeOf(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, clierr.CodeUsage)
	}
}

func TestTooManyArgumentsExitsWithTheUsageCode(t *testing.T) {
	err := execTree(t, "node", "use", "devnet", "extra")
	if err == nil {
		t.Fatal("a surplus argument must be an error")
	}
	if got := clierr.CodeOf(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, clierr.CodeUsage)
	}
}

// Cobra returns help and exits zero for a command it considers not runnable,
// and it decides that before validating arguments. `orama node <typo>` printed
// the group's help and succeeded, so a script never learned the subcommand did
// not exist.
func TestUnknownSubcommandExitsWithTheUsageCode(t *testing.T) {
	err := execTree(t, "node", "definitely-not-a-subcommand")
	if err == nil {
		t.Fatal("an unknown subcommand must be an error")
	}
	if got := clierr.CodeOf(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want %d", got, clierr.CodeUsage)
	}
	if !strings.Contains(err.Error(), "definitely-not-a-subcommand") {
		t.Errorf("the error must name what was typed: %v", err)
	}
}

// A group with no arguments still prints its help and succeeds.
func TestGroupWithNoArgumentsPrintsHelp(t *testing.T) {
	root := newTestTree()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"node"})

	if err := root.Execute(); err != nil {
		t.Fatalf("`orama node` must print help and succeed, got: %v", err)
	}
	if out.Len() == 0 {
		t.Error("`orama node` printed nothing")
	}
}

// classifyUsageErrors must not turn a success into a failure.
func TestValidCommandLineIsNotAnError(t *testing.T) {
	if err := execTree(t, "node", "use", "devnet"); err != nil {
		t.Fatalf("a valid command line must not error: %v", err)
	}
	if err := execTree(t, "node", "push"); err != nil {
		t.Fatalf("a valid command line must not error: %v", err)
	}
}

// The root's --json is what every command answers to. Three commands used to
// implement it for themselves and the rest had no machine-readable output.
func TestRootDefinesJSONForEveryCommand(t *testing.T) {
	root := newRootCmd()
	if root.PersistentFlags().Lookup("json") == nil {
		t.Fatal("--json must be a persistent flag on the root")
	}

	var leaves []*cobra.Command
	walk(root, func(c *cobra.Command) {
		if !c.HasSubCommands() {
			leaves = append(leaves, c)
		}
	})
	if len(leaves) == 0 {
		t.Fatal("no leaf commands found")
	}
	for _, leaf := range leaves {
		if leaf.InheritedFlags().Lookup("json") == nil {
			t.Errorf("`orama %s` does not inherit --json", leaf.CommandPath())
		}
	}
}
