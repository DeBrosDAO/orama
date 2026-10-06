package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The CLI prints copy-pasteable commands in success output, error hints and
// flag help. Several of them named commands that were never registered —
// `orama install` when the command is `orama node install`, `orama invite` on
// the critical path of every node join — so an operator following the output
// hit "unknown command" at the worst moment.
//
// This walks every string literal in the tree that looks like an orama command
// and checks it against the actual cobra command tree, so a rename cannot
// silently strand the text that tells people what to type.

// commandPathRe matches an "orama <words>" mention inside a string literal,
// capturing the words that could be a command path.
var commandPathRe = regexp.MustCompile(`orama ((?:[a-z][a-z0-9-]*)(?: [a-z][a-z0-9-]*)*)`)

func TestPrintedCommandsExist(t *testing.T) {
	root := newRootCmd()
	valid, names := commandPaths(root)

	var problems []string
	for _, dir := range linkedPackageDirs(t) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, line := range strings.Split(string(src), "\n") {
				// Only text the CLI prints. A comment is never shown to an
				// operator, and prose like "the orama service user" would
				// otherwise read as a command.
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				for _, mention := range commandPathRe.FindAllStringSubmatch(line, -1) {
					if cmd, ok := unreachableCommand(mention[1], valid, names); ok {
						problems = append(problems, filepath.Base(dir)+"/"+e.Name()+": \"orama "+cmd+"\" is not a registered command")
					}
				}
			}
		}
	}

	for _, p := range problems {
		t.Error(p)
	}
}

// linkedPackageDirs returns the source directories of every package that links
// into the CLI binary.
//
// Scoping to these is what makes the check meaningful and self-maintaining: a
// string can only be printed if its package ships, and packages that no longer
// ship drop out of the check on their own rather than needing an exclusion
// list.
func linkedPackageDirs(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.Dir}}", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	module, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		dir := strings.TrimSpace(line)
		// Only this module's own packages; the standard library and
		// dependencies do not print orama commands.
		if dir != "" && strings.HasPrefix(dir, module+string(filepath.Separator)) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// unreachableCommand reports a mention that names a real command by a path the
// CLI does not accept.
//
// A mention is only judged when its first word is a command name that exists
// somewhere in the tree — that filters out English ("orama binary", "orama
// directory") while keeping the defect class that matters: a subcommand
// printed as if it were top-level, like "orama install" when the command is
// "orama node install".
//
// The longest run of words that forms a registered path is taken as the
// command, so trailing arguments and flags are ignored.
func unreachableCommand(mention string, valid, names map[string]bool) (string, bool) {
	words := strings.Fields(mention)
	if len(words) == 0 || !names[words[0]] {
		return "", false
	}

	for n := len(words); n > 0; n-- {
		if valid[strings.Join(words[:n], " ")] {
			return "", false
		}
	}
	return words[0], true
}

// commandPaths returns every command path reachable from the root ("node
// install"), and separately every command name used anywhere in the tree
// ("install"). The first says what a user may type; the second says which
// words are commands at all.
func commandPaths(root *cobra.Command) (paths, names map[string]bool) {
	paths = map[string]bool{}
	names = map[string]bool{}
	var walk func(c *cobra.Command, prefix []string)
	walk = func(c *cobra.Command, prefix []string) {
		for _, sub := range c.Commands() {
			name := strings.Fields(sub.Use)[0]
			full := append(append([]string{}, prefix...), name)
			paths[strings.Join(full, " ")] = true
			names[name] = true
			for _, alias := range sub.Aliases {
				names[alias] = true
				paths[strings.Join(append(append([]string{}, prefix...), alias), " ")] = true
			}
			walk(sub, full)
		}
	}
	walk(root, nil)
	return paths, names
}
