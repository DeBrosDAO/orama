package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The CLI reference is whitepaper appendix D, rendered from the cobra tree by
// book_reference_test.go; the test there fails when the file and the tree
// disagree. This file holds what the renderer shares with the other tests.
//
// Hand-written command documentation drifts the moment a flag is added: the
// deployment guide's flag tables were missing --environment, --ssh-user,
// --ca-fingerprint and --leader-raft-addr, and `orama push` and `orama rollout`
// existed without being mentioned anywhere. A reference nobody writes cannot go
// stale that way.
//
// Regenerate with:
//
//	make -C core docs
var updateReference = flag.Bool("update-cli-reference", false, "rewrite the whitepaper CLI reference (appendix D) from the command tree")

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../core/cmd/orama -> repo root
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// firstDifference reports the first differing line, which is all anyone needs
// to see what moved.
func firstDifference(have, want string) string {
	haveLines := strings.Split(have, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(haveLines) && i < len(wantLines); i++ {
		if haveLines[i] != wantLines[i] {
			return fmt.Sprintf("\nfirst difference at line %d:\n  committed: %q\n  generated: %q", i+1, haveLines[i], wantLines[i])
		}
	}
	return fmt.Sprintf("\nthe files differ in length: committed %d lines, generated %d", len(haveLines), len(wantLines))
}

// collectCommands returns every runnable or group command, depth-first and
// alphabetical, excluding the root and cobra's generated help commands.
func collectCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		children := append([]*cobra.Command(nil), cmd.Commands()...)
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			out = append(out, child)
			walk(child)
		}
	}
	walk(root)
	return out
}

// usageLine is the command path plus whatever argument shape its Use string
// declares, with a [flags] marker when it defines flags of its own.
func usageLine(cmd *cobra.Command) string {
	line := cmd.CommandPath()
	if parts := strings.Fields(cmd.Use); len(parts) > 1 {
		args := strings.Join(parts[1:], " ")
		args = strings.TrimSpace(strings.ReplaceAll(args, "[flags]", ""))
		if args != "" {
			line += " " + args
		}
	}
	if hasOwnFlags(cmd) {
		line += " [flags]"
	}
	return line
}

func hasOwnFlags(cmd *cobra.Command) bool {
	return len(ownFlags(cmd)) > 0
}

// ownFlags returns the flags a command declares itself, excluding every flag it
// inherits from an ancestor.
//
// It cannot ask cobra for this. Running a command merges its inherited flags
// into its own set and leaves them there, so LocalNonPersistentFlags answers
// differently depending on whether something earlier in the process executed
// that command — and a reference whose content depends on test ordering is not
// a reference. Subtracting the ancestors' persistent flags by name gives the
// same answer either way.
func ownFlags(cmd *cobra.Command) []*pflag.Flag {
	// cobra injects --help into a command's own flag set the first time that
	// command runs, so it appears or not depending on what else the process did.
	inherited := map[string]bool{"help": true}
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		parent.PersistentFlags().VisitAll(func(f *pflag.Flag) { inherited[f.Name] = true })
	}

	// A command's own persistent flags belong to it, but cobra only merges them
	// into Flags() when the command runs, so both sets are read and deduplicated.
	seen := map[string]bool{}
	var own []*pflag.Flag
	collect := func(f *pflag.Flag) {
		if f.Hidden || inherited[f.Name] || seen[f.Name] {
			return
		}
		seen[f.Name] = true
		own = append(own, f)
	}
	cmd.PersistentFlags().VisitAll(collect)
	cmd.Flags().VisitAll(collect)
	return own
}

func renderFlags(flags []*pflag.Flag) string {
	var rows []string
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		def := f.DefValue
		if def == "" || def == "[]" {
			def = "—"
		} else {
			def = "`" + def + "`"
		}
		usage := strings.ReplaceAll(f.Usage, "|", "\\|")
		usage = strings.ReplaceAll(usage, "\n", " ")
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |\n", name, def, usage))
	}
	sort.Strings(rows)
	return strings.Join(rows, "")
}
