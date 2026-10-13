//go:build e2e_fleet

package docsclaims

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// Documents the checks read (repo-relative): website docs pages, whitepaper
// chapters and appendices, and the one plan.
const (
	webrtcDoc   = "website/src/docs/developer/webrtc.mdx"
	capDoc      = "docs/whitepaper/technical-reference/vol1/09-namespaces.md"
	surfaceDoc  = "docs/whitepaper/technical-reference/appendices/i-api-surface.md"
	chainDoc    = "docs/whitepaper/technical-reference/vol2/39-chain-architecture.md"
	economyDoc  = "docs/whitepaper/technical-reference/vol2/40-economics.md"
	cliRefDoc   = cliconf.ReferencePath
	whitepaper  = "docs/whitepaper/WHITEPAPER.md"
	openNetPlan = "plans/open-network.md"
)

// websiteDocsDir holds the website's docs pages (MDX), the other place the
// documentation lives besides the whitepaper under docs/.
const websiteDocsDir = "website/src/docs"

// line is one line of a document, for "file:line" in failure messages.
type line struct {
	File string
	N    int
	Text string
}

func (l line) String() string {
	return fmt.Sprintf("%s:%d: %s", l.File, l.N, strings.TrimSpace(l.Text))
}

// lines returns every line of rel.
func lines(t testing.TB, rel string) []line {
	t.Helper()
	var out []line
	for i, s := range strings.Split(cliconf.ReadRepoFile(t, rel), "\n") {
		out = append(out, line{File: rel, N: i + 1, Text: s})
	}
	return out
}

// grep returns the lines of rel that match re.
func grep(t testing.TB, rel string, re *regexp.Regexp) []line {
	t.Helper()
	var out []line
	for _, l := range lines(t, rel) {
		if re.MatchString(l.Text) {
			out = append(out, l)
		}
	}
	return out
}

// docs returns every documentation page, repo-relative, sorted: the Markdown
// under docs/ (the whitepaper) and the MDX under website/src/docs.
func docs(t testing.TB) []string {
	t.Helper()
	root := cliconf.RepoRoot(t)
	var out []string
	for dir, suffix := range map[string]string{"docs": ".md", websiteDocsDir: ".mdx"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, suffix) {
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					return relErr
				}
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to list %s: %v", dir, err)
		}
	}
	sort.Strings(out)
	return out
}

// codeConst reads `name = <value>` from a Go source file in the checkout, so
// a doc is compared with the value the code really has.
func codeConst(t testing.TB, rel, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*(.+?)\s*(//.*)?$`)
	m := re.FindStringSubmatch(cliconf.ReadRepoFile(t, rel))
	if m == nil {
		t.Fatalf("%s no longer defines %s: update this check to where the value lives now", rel, name)
	}
	return m[1]
}

// errStatus is a non-2xx answer as an observation for eventually.
func errStatus(resp *gw.Response) error {
	return fmt.Errorf("HTTP %d: %.200s", resp.Status, resp.Body)
}
