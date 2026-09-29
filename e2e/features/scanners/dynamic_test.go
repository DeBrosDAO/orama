//go:build e2e_fleet

package scanners

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

const (
	// fuzzTime is how long each Fuzz target runs: a smoke run that proves the
	// target builds, its seed corpus passes and a short random search finds
	// nothing, not a campaign.
	fuzzTime = "20s"
	// fuzzRoot is where the targets are looked for, relative to core.
	fuzzRoot = "pkg"
	// raceTimeout is go test's own timeout for the race run (below raceBudget).
	raceTimeout = "40m"
)

// fuzzFunc matches a fuzz target's declaration.
var fuzzFunc = regexp.MustCompile(`(?m)^func (Fuzz[A-Za-z0-9_]*)\(\s*\w+ \*testing\.F\s*\)`)

// racePackages start the most goroutines: every request, subscription,
// replication and reconcile loop of a node runs in them.
var racePackages = []string{"./pkg/gateway/...", "./pkg/rqlite/...", "./pkg/namespace/...", "./pkg/node/..."}

// fuzzTarget is one Fuzz function and the package (relative to core) it is in.
type fuzzTarget struct {
	pkg  string
	name string
}

// fuzzTargets finds every Fuzz function under core/pkg.
func fuzzTargets(t *testing.T, core string) []fuzzTarget {
	t.Helper()
	var out []fuzzTarget
	err := filepath.WalkDir(filepath.Join(core, fuzzRoot), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(core, filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, m := range fuzzFunc.FindAllStringSubmatch(string(src), -1) {
			out = append(out, fuzzTarget{pkg: "./" + filepath.ToSlash(rel), name: m[1]})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to look for Fuzz targets under core/%s: %v", fuzzRoot, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pkg+out[i].name < out[j].pkg+out[j].name })
	return out
}

// TestFuzz_targetsSmoke runs every Fuzz target of core/pkg for fuzzTime.
// One target at a time: go test fuzzes one target per invocation and the
// search takes every core.
func TestFuzz_targetsSmoke(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "the fuzz targets run with go test -fuzz")
	s := newScan(t)
	core := filepath.Join(s.root, "core")
	targets := fuzzTargets(t, core)
	if len(targets) == 0 {
		harness.SkipNotApplicable(t, "core/pkg has no Fuzz functions (func FuzzX(f *testing.F)); the smoke run applies once one exists")
	}
	for _, ft := range targets {
		t.Run(ft.name, func(t *testing.T) {
			res := s.run(t, "core", fuzzBudget, "go", "test", "-run", "^$", "-fuzz", "^"+ft.name+"$", "-fuzztime", fuzzTime, ft.pkg)
			if res.Exit != 0 {
				t.Errorf("%s in %s failed (exit %d); a new failing input is under testdata/fuzz:\n%s", ft.name, ft.pkg, res.Exit, realistic.Tail(res.Output()))
			}
		})
	}
}

// TestRace_concurrentPackages: the unit tests of the goroutine-heavy
// packages pass under the race detector.
func TestRace_concurrentPackages(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "the race run is go test -race")
	s := newScan(t)
	args := append([]string{"test", "-race", "-count=1", "-timeout", raceTimeout}, racePackages...)
	res := s.run(t, "core", raceBudget, "go", args...)
	if res.Exit != 0 {
		races := strings.Count(res.Output(), "WARNING: DATA RACE")
		t.Errorf("go test -race over %v failed (exit %d, %d data race report(s)):\n%s", racePackages, res.Exit, races, realistic.Tail(res.Output()))
	}
}
