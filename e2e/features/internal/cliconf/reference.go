//go:build e2e_fleet

package cliconf

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ReferencePath is the generated command reference, whitepaper appendix D
// (core/cmd/orama/book_reference_test.go renders it).
const ReferencePath = "docs/whitepaper/technical-reference/appendices/d-cli-reference.md"

// Command is one `## orama ...` section of the reference.
type Command struct {
	// Path is "orama app env set".
	Path string
	// Line is the heading's line number in the reference, for failure messages.
	Line  int
	Short string
	// Usage is the usage line, e.g. "orama env add <name> <gateway_url> [description] [flags]".
	Usage       string
	Aliases     []string
	Flags       []string
	Subcommands []string
}

// Args is the command path without "orama", as the CLI takes it.
func (c Command) Args() []string { return strings.Fields(c.Path)[1:] }

// Group reports whether the command has subcommands.
func (c Command) Group() bool { return len(c.Subcommands) > 0 }

// Anchor is where the command is documented, for failure messages.
func (c Command) Anchor() string {
	return fmt.Sprintf("%s:%d (#%s)", ReferencePath, c.Line, strings.ReplaceAll(c.Path, " ", "-"))
}

// Reference is the parsed CLI reference, in document order.
type Reference struct {
	Commands []Command
	byPath   map[string]Command
}

// Get returns the command documented at path.
func (r *Reference) Get(path string) (Command, bool) {
	c, ok := r.byPath[path]
	return c, ok
}

// Under returns path itself and every command below it, in document order.
func (r *Reference) Under(path string) []Command {
	var out []Command
	for _, c := range r.Commands {
		if c.Path == path || strings.HasPrefix(c.Path, path+" ") {
			out = append(out, c)
		}
	}
	return out
}

// Paths returns every documented command path, sorted.
func (r *Reference) Paths() []string {
	out := make([]string, 0, len(r.Commands))
	for _, c := range r.Commands {
		out = append(out, c.Path)
	}
	sort.Strings(out)
	return out
}

// LoadReference parses the CLI reference (ReferencePath) from the checkout.
func LoadReference(t testing.TB) *Reference {
	t.Helper()
	ref, err := ParseReference(ReadRepoFile(t, ReferencePath))
	if err != nil {
		t.Fatalf("%s: %v", ReferencePath, err)
	}
	return ref
}

var (
	headingRe = regexp.MustCompile("^## (orama(?: [a-z0-9-]+)*)$")
	tickRe    = regexp.MustCompile("`([^`]+)`")
	// unescapeProse undoes the entities the book renderer writes outside code
	// (bookCell in core/cmd/orama/book_reference_test.go).
	unescapeProse = strings.NewReplacer("&#123;", "{", "&#125;", "}", "&lt;", "<")
)

const (
	flagTableHeader = "| Flag | Default | Description |"
	aliasesPrefix   = "Aliases: "
	subsPrefix      = "Subcommands: "
	fence           = "```"
	sectionPrefix   = "## "
)

// ParseReference reads every command section of the reference.
func ParseReference(text string) (*Reference, error) {
	ref := &Reference{byPath: map[string]Command{}}
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := headingRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		end := sectionEnd(lines, i+1)
		c, err := parseSection(m[1], i+1, lines[i+1:end])
		if err != nil {
			return nil, err
		}
		if _, dup := ref.byPath[c.Path]; dup {
			return nil, fmt.Errorf("line %d: %s is documented twice", i+1, c.Path)
		}
		ref.byPath[c.Path] = c
		ref.Commands = append(ref.Commands, c)
		i = end - 1
	}
	if len(ref.Commands) == 0 {
		return nil, fmt.Errorf("no `## orama ...` command sections")
	}
	return ref, nil
}

// sectionEnd is the line of the next "## " heading after from. A line inside a
// fenced block is never a heading: a command's long help is fenced.
func sectionEnd(lines []string, from int) int {
	inFence := false
	for end := from; end < len(lines); end++ {
		switch {
		case strings.HasPrefix(lines[end], fence):
			inFence = !inFence
		case !inFence && strings.HasPrefix(lines[end], sectionPrefix):
			return end
		}
	}
	return len(lines)
}

func parseSection(path string, line int, body []string) (Command, error) {
	c := Command{Path: path, Line: line}
	// prose is set by the first line after the usage block that is not the
	// aliases line: the generator writes "Aliases: `a`" right after the usage
	// block, before the Long text, so an "Aliases: ..." in the Long text is
	// prose, not the command's aliases. The Long text is a fenced block and is
	// skipped whole.
	inTable, sawUsage, prose := false, false, false
	for i := 0; i < len(body); i++ {
		l := body[i]
		switch {
		case !sawUsage && strings.HasPrefix(l, fence) && i+1 < len(body):
			c.Usage, sawUsage = strings.TrimSpace(body[i+1]), true
			i += 2
		case sawUsage && strings.HasPrefix(l, fence):
			prose = true
			for i++; i < len(body) && !strings.HasPrefix(body[i], fence); i++ {
			}
		case c.Short == "" && !sawUsage && strings.TrimSpace(l) != "":
			c.Short = unescapeProse.Replace(strings.TrimSpace(l))
		case sawUsage && !prose && strings.HasPrefix(l, aliasesPrefix):
			c.Aliases = ticked(l)
		case strings.HasPrefix(l, subsPrefix):
			c.Subcommands, prose = ticked(l), true
		case l == flagTableHeader:
			inTable, prose = true, true
		case inTable && strings.HasPrefix(l, "| `"):
			c.Flags = append(c.Flags, flagNames(l)...)
		case inTable && !strings.HasPrefix(l, "|"):
			inTable = false
		case sawUsage && strings.TrimSpace(l) != "":
			prose = true
		}
	}
	if !sawUsage {
		return c, fmt.Errorf("line %d: %s has no usage block", line, path)
	}
	sort.Strings(c.Flags)
	sort.Strings(c.Subcommands)
	sort.Strings(c.Aliases)
	return c, nil
}

// flagNames returns the flag spellings in a table row's first cell, e.g.
// "| `-n`, `--lines` | ..." -> ["--lines", "-n"].
func flagNames(row string) []string {
	cells := strings.Split(row, " | ")
	return ticked(strings.TrimPrefix(cells[0], "| "))
}

func ticked(s string) []string {
	var out []string
	for _, m := range tickRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
