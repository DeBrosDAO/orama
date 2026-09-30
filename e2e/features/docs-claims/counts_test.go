//go:build e2e_fleet

package docsclaims

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	// surfaceHeader is "it reaches <sdk> of <total> routes, and the other <rest>".
	surfaceHeader = regexp.MustCompile(`reaches\s+(\d+)\s+of\s+(\d+)\s+routes,\s+and\s+the\s+other\s+(\d+)`)
	// ownerRow is a row of the owner table: | `SDK` | meaning | 38 |.
	ownerRow = regexp.MustCompile("^\\| `(SDK|CLI|direct|internal)` \\|.*\\| (\\d+) \\|$")
	// routeRow is a route row: | `/v1/...` | OWNER | why |.
	routeRow = regexp.MustCompile("^\\| `(/[^`]*)` \\| (\\w+) \\|")
)

// TestAPISurface_headerCountsMatchRows: the counts API_SURFACE.md states (the
// "reaches N of M routes" sentence and the owner table) must be the number of
// route rows the document actually has, per owner (bugboard 2855: the header
// said 152 while 167 routes were listed).
func TestAPISurface_headerCountsMatchRows(t *testing.T) {
	t.Parallel()
	rows := map[string]int{}
	total := 0
	seen := map[string]bool{}
	stated := map[string]int{}
	var header []string
	for _, l := range lines(t, surfaceDoc) {
		if m := routeRow.FindStringSubmatch(l.Text); m != nil {
			if seen[m[1]] {
				t.Errorf("%s\n  lists %s twice", l, m[1])
			}
			seen[m[1]] = true
			rows[m[2]]++
			total++
		}
		if m := ownerRow.FindStringSubmatch(strings.TrimSpace(l.Text)); m != nil {
			stated[m[1]] = atoi(m[2])
		}
	}
	if m := surfaceHeader.FindStringSubmatch(strings.Join(texts(lines(t, surfaceDoc)), "\n")); m != nil {
		header = m[1:]
	}
	if total == 0 || len(header) != 3 {
		t.Fatalf("%s: found %d route rows and header %v; the document's shape changed", surfaceDoc, total, header)
	}
	sdk, all, rest := atoi(header[0]), atoi(header[1]), atoi(header[2])
	if all != total || sdk != rows["SDK"] || rest != total-rows["SDK"] {
		t.Errorf("%s says the SDK reaches %d of %d routes and %d are elsewhere; the rows are %d, %d SDK", surfaceDoc, sdk, all, rest, total, rows["SDK"])
	}
	for owner, n := range stated {
		if rows[owner] != n {
			t.Errorf("%s owner table: %s count %d, but %d rows are %s", surfaceDoc, owner, n, rows[owner], owner)
		}
	}
	for owner, n := range rows {
		if _, ok := stated[owner]; !ok {
			t.Errorf("%s: %d rows are owned by %q, which the owner table does not define", surfaceDoc, n, owner)
		}
	}
}

// atoi reads a number the pattern already matched as \d+.
func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic("docsclaims: " + s + " matched \\d+ but is not a number")
	}
	return n
}
