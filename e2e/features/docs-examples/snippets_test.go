//go:build e2e_fleet

package docsexamples

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
)

// exampleDocs are the documents whose examples a user copies
// (bugboard 2866, E2E-46).
var exampleDocs = []string{
	"README.md", "sdk/README.md", "sdk/QUICKSTART.md",
	"docs/DEPLOYMENT_GUIDE.md", "docs/GO_CLIENT_SDK.md", "docs/TS_SDK.md", "docs/SERVERLESS.md",
}

// snippet is one fenced code block.
type snippet struct {
	File string
	// Line is the line of the opening fence.
	Line int
	Lang string
	Body string
}

func (s snippet) Where() string { return fmt.Sprintf("%s:%d", s.File, s.Line) }

// fenceRe is an opening fence, indented or not (a block inside a list item
// is indented by the item's depth): its indent, backticks and language.
var fenceRe = regexp.MustCompile("^([ \t]*)(`{3,})([A-Za-z0-9_+-]*)")

// snippets returns every fenced block of rel whose language is one of langs,
// with the opening fence's indent taken off each line of its body.
func snippets(t testing.TB, rel string, langs ...string) []snippet {
	t.Helper()
	want := map[string]bool{}
	for _, l := range langs {
		want[l] = true
	}
	lines := strings.Split(cliconf.ReadRepoFile(t, rel), "\n")
	var out []snippet
	for i := 0; i < len(lines); i++ {
		m := fenceRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		body, closed := fenceBody(lines[i+1:], m[1], m[2])
		if !closed {
			t.Fatalf("%s: a code fence opened at line %d never closes", rel, i+1)
		}
		if want[m[3]] {
			out = append(out, snippet{File: rel, Line: i + 1, Lang: m[3], Body: strings.Join(body, "\n") + "\n"})
		}
		i += len(body) + 1
	}
	return out
}

// fenceBody collects the lines up to the closing fence (at least ticks
// backticks and nothing else, at any indent), each without indent.
func fenceBody(lines []string, indent, ticks string) (body []string, closed bool) {
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, ticks) && strings.Trim(trimmed, "`") == "" {
			return body, true
		}
		body = append(body, strings.TrimPrefix(l, indent))
	}
	return body, false
}

// allSnippets is snippets over every example document.
func allSnippets(t testing.TB, langs ...string) []snippet {
	t.Helper()
	var out []snippet
	for _, f := range exampleDocs {
		out = append(out, snippets(t, f, langs...)...)
	}
	return out
}

// TestDocsExamples_jsonBlocksParse: every block tagged json is JSON; a block
// with comments or "..." should be tagged jsonc or text, or a reader copying
// it gets a parse error.
func TestDocsExamples_jsonBlocksParse(t *testing.T) {
	t.Parallel()
	blocks := allSnippets(t, "json")
	if len(blocks) == 0 {
		// docs/SERVERLESS.md has json blocks: none found means the fence
		// parsing broke, and passing would check nothing.
		t.Fatalf("no json block found in %v: the fence parsing or the documents changed", exampleDocs)
	}
	for _, s := range blocks {
		var v any
		if err := json.Unmarshal([]byte(s.Body), &v); err != nil {
			t.Errorf("%s: the json block does not parse: %v", s.Where(), err)
		}
	}
}
