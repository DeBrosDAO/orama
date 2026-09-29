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

var fenceRe = regexp.MustCompile("(?m)^```([A-Za-z0-9_+-]*)[^\\n]*\\n")

// snippets returns every fenced block of rel whose language is one of langs.
func snippets(t testing.TB, rel string, langs ...string) []snippet {
	t.Helper()
	text := cliconf.ReadRepoFile(t, rel)
	want := map[string]bool{}
	for _, l := range langs {
		want[l] = true
	}
	var out []snippet
	for pos := 0; ; {
		open := fenceRe.FindStringSubmatchIndex(text[pos:])
		if open == nil {
			return out
		}
		start := pos + open[1]
		end := strings.Index(text[start:], "\n```")
		if end < 0 {
			t.Fatalf("%s: a code fence opened at line %d never closes", rel, strings.Count(text[:pos+open[0]], "\n")+1)
		}
		lang := text[pos+open[2] : pos+open[3]]
		if want[lang] {
			out = append(out, snippet{File: rel, Line: strings.Count(text[:pos+open[0]], "\n") + 1, Lang: lang, Body: text[start : start+end+1]})
		}
		pos = start + end + len("\n```")
	}
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
	for _, s := range blocks {
		var v any
		if err := json.Unmarshal([]byte(s.Body), &v); err != nil {
			t.Errorf("%s: the json block does not parse: %v", s.Where(), err)
		}
	}
}
