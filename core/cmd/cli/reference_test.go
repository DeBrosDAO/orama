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

// docs/CLI_REFERENCE.md is rendered from the cobra tree, and this test fails
// when the file and the tree disagree.
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
var updateReference = flag.Bool("update-cli-reference", false, "rewrite docs/CLI_REFERENCE.md from the command tree")

func TestCLIReferenceMatchesTheCommandTree(t *testing.T) {
	rendered := renderReference(newRootCmd())
	path := filepath.Join(repoRoot(t), "docs/CLI_REFERENCE.md")

	if *updateReference {
		if err := os.WriteFile(path, []byte(rendered), 0644); err != nil {
			t.Fatalf("write reference: %v", err)
		}
		t.Logf("wrote %s", path)
		return
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reference: %v (run `make -C core docs`)", err)
	}

	if string(existing) != rendered {
		t.Errorf("docs/CLI_REFERENCE.md does not match the command tree.\n"+
			"Run `make -C core docs` and commit the result.\n%s", firstDifference(string(existing), rendered))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// .../core/cmd/cli -> repo root
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

func renderReference(root *cobra.Command) string {
	var b strings.Builder

	b.WriteString(`<!--
Generated from the cobra command tree by core/cmd/cli/reference_test.go.
Do not edit by hand: run ` + "`make -C core docs`" + `.
-->

# CLI reference

Every command the ` + "`orama`" + ` binary defines, with its flags. Generated from the
command tree, so it cannot drift from the code: a test fails when this file and
the tree disagree.

Task-shaped documentation lives elsewhere — [deploying apps](DEPLOYMENT_GUIDE.md),
[building and rolling out](DEV_DEPLOY.md), [functions](SERVERLESS.md). This page
is the index.

`)

	commands := collectCommands(root)

	b.WriteString("## Commands\n\n")
	for _, cmd := range commands {
		path := cmd.CommandPath()
		anchor := strings.ReplaceAll(path, " ", "-")
		// "orama app" is depth 0 in this list; "orama app env" is depth 1.
		depth := strings.Count(path, " ") - 1
		b.WriteString(fmt.Sprintf("%s- [`%s`](#%s) — %s\n",
			strings.Repeat("  ", depth), path, anchor, cmd.Short))
	}
	b.WriteString("\n---\n\n")

	for _, cmd := range commands {
		b.WriteString(renderCommand(cmd))
	}

	return b.String()
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

func renderCommand(cmd *cobra.Command) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("### %s\n\n", cmd.CommandPath()))
	if cmd.Short != "" {
		b.WriteString(cmd.Short + "\n\n")
	}

	// Not cobra's UseLine: it reports whether a command "has available flags",
	// which cobra computes lazily and caches, so the answer depends on whether
	// something earlier in the process happened to touch that command. A
	// reference whose content depends on test ordering is not a reference.
	b.WriteString("```\n" + usageLine(cmd) + "\n```\n\n")

	if len(cmd.Aliases) > 0 {
		b.WriteString(fmt.Sprintf("Aliases: %s\n\n", "`"+strings.Join(cmd.Aliases, "`, `")+"`"))
	}

	if long := strings.TrimSpace(cmd.Long); long != "" && long != strings.TrimSpace(cmd.Short) {
		b.WriteString(long + "\n\n")
	}

	if flags := renderFlags(ownFlags(cmd)); flags != "" {
		b.WriteString("| Flag | Default | Description |\n|------|---------|-------------|\n")
		b.WriteString(flags)
		b.WriteString("\n")
	}

	if cmd.HasAvailableSubCommands() {
		var names []string
		for _, child := range cmd.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			names = append(names, "`"+child.Name()+"`")
		}
		sort.Strings(names)
		if len(names) > 0 {
			b.WriteString("Subcommands: " + strings.Join(names, ", ") + "\n\n")
		}
	}

	return b.String()
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
