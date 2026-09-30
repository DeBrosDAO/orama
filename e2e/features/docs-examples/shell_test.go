//go:build e2e_fleet

package docsexamples

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// commandLine is one `orama ...` invocation in a shell block.
type commandLine struct {
	Where string
	Args  []string // without "orama"
}

var (
	segmentSplit = regexp.MustCompile(`&&|\|\||;|\|`)
	envAssign    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=`)
)

// oramaLines returns every orama invocation in bash/sh blocks: continuation
// lines joined, comments dropped, pipelines and && chains split, `sudo`,
// `$(`, `export X=` and VAR=value prefixes removed.
func oramaLines(t testing.TB) []commandLine {
	t.Helper()
	var out []commandLine
	for _, s := range allSnippets(t, "bash", "sh", "shell") {
		for _, lg := range joinContinuations(s.Body) {
			for _, seg := range segmentSplit.Split(stripComment(lg.text), -1) {
				words := leadingNoise(tokenize(seg))
				if len(words) > 0 && words[0] == "orama" {
					out = append(out, commandLine{Where: fmt.Sprintf("%s:%d", s.File, s.Line+1+lg.line), Args: words[1:]})
				}
			}
		}
	}
	return out
}

// leadingNoise drops what can come before orama on a line: sudo, export,
// VAR=value assignments and a command substitution's "$(".
func leadingNoise(words []string) []string {
	for len(words) > 0 {
		w := words[0]
		if i := strings.Index(w, "$(orama"); i >= 0 {
			inner := strings.TrimSuffix(w[i+len("$("):], ")")
			return tokenize(inner)
		}
		if w != "sudo" && w != "export" && !envAssign.MatchString(w) {
			return words
		}
		words = words[1:]
	}
	return words
}

// logical is a shell line with its continuations joined, and the line of
// the block it starts on.
type logical struct {
	line int
	text string
}

func joinContinuations(body string) []logical {
	var out []logical
	var cur strings.Builder
	start := 0
	for i, l := range strings.Split(body, "\n") {
		if cur.Len() == 0 {
			start = i
		}
		if trimmed := strings.TrimRight(l, " "); strings.HasSuffix(trimmed, "\\") {
			cur.WriteString(strings.TrimSuffix(trimmed, "\\") + " ")
			continue
		}
		cur.WriteString(l)
		out = append(out, logical{line: start, text: cur.String()})
		cur.Reset()
	}
	return out
}

func stripComment(l string) string {
	if i := strings.Index(l, " #"); i >= 0 {
		l = l[:i]
	}
	if strings.HasPrefix(strings.TrimSpace(l), "#") {
		return ""
	}
	return l
}

// tokenize splits on spaces outside single or double quotes, dropping them.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\''):
			quote = r
		case quote == 0 && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// flagSets caches the flags `orama <path> --help` accepts (own and inherited).
type flagSets struct {
	cli   *oramacli.Runner
	cache map[string]map[string]bool
}

func (fs *flagSets) of(t testing.TB, c cliconf.Command) map[string]bool {
	t.Helper()
	if got, ok := fs.cache[c.Path]; ok {
		return got
	}
	h := cliconf.HelpOf(t, fs.cli, c)
	set := map[string]bool{"--help": true, "-h": true}
	for _, f := range append(h.Flags, h.Global...) {
		set[f] = true
	}
	fs.cache[c.Path] = set
	return set
}

// check returns what is wrong with one documented invocation, or "".
func (fs *flagSets) check(t testing.TB, ref *cliconf.Reference, cl commandLine) string {
	t.Helper()
	path, depth := "orama", 0
	var c cliconf.Command
	for _, w := range cl.Args {
		cmd, ok := ref.Get(path + " " + w)
		if !ok {
			break
		}
		path, c, depth = path+" "+w, cmd, depth+1
	}
	if depth == 0 {
		return fmt.Sprintf("`orama %s` names no command", strings.Join(cl.Args, " "))
	}
	if c.Group() && depth < len(cl.Args) && !strings.HasPrefix(cl.Args[depth], "-") {
		return fmt.Sprintf("`%s` has no subcommand %q", path, cl.Args[depth])
	}
	flags := fs.of(t, c)
	for _, a := range cl.Args {
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		name := strings.SplitN(a, "=", 2)[0]
		if !flags[name] {
			return fmt.Sprintf("`%s` has no flag %s", path, name)
		}
	}
	return ""
}

// TestDocsExamples_oramaCommandLinesValid: every `orama ...` line in the
// shell examples a user copies names a command that exists, a subcommand
// its group has, and only flags that command (or the root) accepts. Nothing
// is executed; a line that fails is a doc bug at the file:line named.
func TestDocsExamples_oramaCommandLinesValid(t *testing.T) {
	t.Parallel()
	ref := cliconf.LoadReference(t)
	fs := &flagSets{cli: cliForHelp(t), cache: map[string]map[string]bool{}}
	lines := oramaLines(t)
	if len(lines) == 0 {
		t.Fatal("no orama command in the example documents: the extraction broke")
	}
	for _, cl := range lines {
		if problem := fs.check(t, ref, cl); problem != "" {
			t.Errorf("%s: %s", cl.Where, problem)
		}
	}
}

// cliForHelp is the runner that answers --help (no command runs).
func cliForHelp(t testing.TB) *oramacli.Runner {
	t.Helper()
	return harness.CLI(t)
}
