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
func WalkLive(t testing.TB, cli *oramacli.Runner) []LiveCommand {
	t.Helper()
	var out []LiveCommand
	queue := []string{rootPath}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		args := append(strings.Fields(path)[1:], "--help")
		res := infra.Run(t, cli, args...)
		infra.ExpectExit(t, res, infra.ExitOK)
		h := ParseHelp(res.Stdout)
		for _, sub := range h.Subcommands {
			child := path + " " + sub
			out = append(out, LiveCommand{Path: child, Short: h.Shorts[sub]})
			queue = append(queue, child)
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
