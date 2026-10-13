//go:build e2e_fleet

package cliconf

import (
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// rootPath is the binary's own name, the root of every command path.
const rootPath = "orama"

// LiveCommand is one command the binary's help tree shows.
type LiveCommand struct {
	Path  string
	Short string
}

// WalkLive follows "Available Commands:" from `orama --help` down, and
// returns every command below the root with the one-line description its
// parent lists for it.
//
// A top-level group hidden from `orama --help` (orama maint, and the operator
// groups that setup, status and upgrade replace) is walked too when it is
// named in hidden: nothing lists it, so it is added with its documented
// description, and its own subcommands are read from its help as usual.
func WalkLive(t testing.TB, cli *oramacli.Runner, hidden ...Command) []LiveCommand {
	t.Helper()
	var out []LiveCommand
	queue := []string{rootPath}
	listed := map[string]bool{}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		args := append(strings.Fields(path)[1:], "--help")
		res := infra.Run(t, cli, args...)
		infra.ExpectExit(t, res, infra.ExitOK)
		h := ParseHelp(res.Stdout)
		for _, sub := range h.Subcommands {
			child := path + " " + sub
			listed[child] = true
			out = append(out, LiveCommand{Path: child, Short: h.Shorts[sub]})
			queue = append(queue, child)
		}
		if path == rootPath {
			for _, c := range hidden {
				if !listed[c.Path] {
					out = append(out, LiveCommand{Path: c.Path, Short: c.Short})
					queue = append(queue, c.Path)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// TreeDiff compares the live tree with the reference: commands only one of
// them has, and one-line descriptions that differ.
func TreeDiff(ref *Reference, live []LiveCommand) (onlyLive, onlyDoc, shorts []string) {
	seen := map[string]bool{}
	for _, l := range live {
		seen[l.Path] = true
		c, ok := ref.Get(l.Path)
		if !ok {
			onlyLive = append(onlyLive, l.Path)
			continue
		}
		if c.Short != l.Short {
			shorts = append(shorts, l.Path+": reference "+quote(c.Short)+", binary "+quote(l.Short))
		}
	}
	for _, p := range ref.Paths() {
		if c, _ := ref.Get(p); !seen[p] && !strings.HasPrefix(c.Short, deprecatedPrefix) {
			onlyDoc = append(onlyDoc, p)
		}
	}
	return onlyLive, onlyDoc, shorts
}

func quote(s string) string { return "\"" + s + "\"" }
