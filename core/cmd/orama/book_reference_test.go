package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Appendix D of the whitepaper Technical Reference is the same command tree as
// docs/CLI_REFERENCE.md, rendered for the book: its Markdown must pass the
// book's MDX rules (no raw braces or angle brackets outside code), and long
// help goes in a fenced block. `make -C core docs` rewrites both files.

const bookReferenceFile = "docs/whitepaper/technical-reference/appendices/d-cli-reference.md"

func bookReferencePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), filepath.FromSlash(bookReferenceFile))
}

func TestBookCLIReferenceMatchesTheCommandTree(t *testing.T) {
	if *updateReference {
		t.Skip("rewritten by TestCLIReferenceMatchesTheCommandTree")
	}
	existing, err := os.ReadFile(bookReferencePath(t))
	if err != nil {
		t.Fatalf("read book reference: %v (run `make -C core docs`)", err)
	}
	rendered := renderBookReference(newRootCmd())
	if string(existing) != rendered {
		t.Errorf("%s does not match the command tree.\nRun `make -C core docs`.\n%s",
			bookReferenceFile, firstDifference(string(existing), rendered))
	}
}

func renderBookReference(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("# CLI reference\n\n> **At a glance.**\n>\n" +
		"> - **Generated** from the `orama` binary's cobra command tree by `make whitepaper-gen`. Do not edit by hand: a test in `core/cmd/orama` fails when this file and the code disagree.\n\n" +
		"Every command the `orama` binary defines, with its flags. [The CLI](../vol1/35-the-cli.md) explains how the binary is built and how commands reach the cluster.\n\n")

	commands := collectCommands(root)
	b.WriteString("## Commands\n\n")
	for _, cmd := range commands {
		path := cmd.CommandPath()
		depth := strings.Count(path, " ") - 1
		fmt.Fprintf(&b, "%s- [`%s`](#%s) - %s\n", strings.Repeat("  ", depth), path,
			strings.ReplaceAll(path, " ", "-"), bookCell(cmd.Short))
	}
	for _, cmd := range commands {
		b.WriteString(renderBookCommand(cmd))
	}
	return b.String()
}

func renderBookCommand(cmd *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n## %s\n\n", cmd.CommandPath())
	if cmd.Short != "" {
		b.WriteString(bookCell(cmd.Short) + "\n\n")
	}
	b.WriteString("```text\n" + usageLine(cmd) + "\n```\n\n")
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(&b, "Aliases: `%s`\n\n", strings.Join(cmd.Aliases, "`, `"))
	}
	if long := strings.TrimSpace(cmd.Long); long != "" && long != strings.TrimSpace(cmd.Short) {
		b.WriteString("```text\n" + long + "\n```\n\n")
	}
	if flags := ownFlags(cmd); len(flags) > 0 {
		b.WriteString("| Flag | Default | Description |\n|---|---|---|\n")
		for _, row := range strings.Split(strings.TrimRight(renderFlags(flags), "\n"), "\n") {
			b.WriteString(bookFlagRow(row) + "\n")
		}
		b.WriteString("\n")
	}
	var names []string
	for _, child := range cmd.Commands() {
		if !child.Hidden && child.Name() != "help" && child.Name() != "completion" {
			names = append(names, "`"+child.Name()+"`")
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		b.WriteString("Subcommands: " + strings.Join(names, ", ") + "\n")
	}
	return b.String()
}

// bookFlagRow rewrites one table row from renderFlags so that braces and angle
// brackets outside code spans become entities. A default sits in a code span
// and stays as it is.
func bookFlagRow(row string) string {
	cells := strings.SplitN(strings.TrimPrefix(row, "| "), " | ", 3)
	if len(cells) != 3 {
		return bookCell(row)
	}
	desc := strings.TrimSuffix(cells[2], " |")
	return "| " + cells[0] + " | " + cells[1] + " | " + bookCell(desc) + " |"
}

// bookCell makes text safe for the website's MDX renderer.
func bookCell(text string) string {
	return strings.NewReplacer("{", "&#123;", "}", "&#125;", "<", "&lt;").Replace(text)
}

func TestBookCell_escapesMDXCharacters(t *testing.T) {
	got := bookCell("use {id} and <ns>")
	if strings.ContainsAny(got, "{}<") {
		t.Errorf("bookCell left an MDX-unsafe character: %q", got)
	}
}

func TestBookFlagRow_keepsColumns(t *testing.T) {
	got := bookFlagRow("| `--ns` | `x` | the <namespace> {name} |")
	want := "| `--ns` | `x` | the &lt;namespace> &#123;name&#125; |"
	if got != want {
		t.Errorf("bookFlagRow = %q, want %q", got, want)
	}
}

func TestRenderBookReference_isMDXSafe(t *testing.T) {
	out := renderBookReference(newRootCmd())
	inCode := false
	for i, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		prose := regexp.MustCompile("`[^`]*`").ReplaceAllString(line, "")
		if strings.ContainsAny(prose, "{}<") {
			t.Errorf("line %d has an MDX-unsafe character outside code: %q", i+1, line)
		}
	}
}
