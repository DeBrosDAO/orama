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

// Documents the checks read (repo-relative).
const (
	webrtcDoc   = "docs/WEBRTC.md"
	securityDoc = "docs/SECURITY.md"
	surfaceDoc  = "docs/API_SURFACE.md"
	chainDoc    = "docs/CHAIN.md"
	cliRefDoc   = "docs/CLI_REFERENCE.md"
	whitepaper  = "docs/whitepaper/WHITEPAPER.md"
	openNetPlan = "plans/open-network.md"
)

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

// docs returns every Markdown file under docs/, repo-relative, sorted.
func docs(t testing.TB) []string {
	t.Helper()
	root := cliconf.RepoRoot(t)
	var out []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				return relErr
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to list docs/: %v", err)
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
