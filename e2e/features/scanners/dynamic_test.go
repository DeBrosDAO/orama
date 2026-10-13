//go:build e2e_fleet

package scanners

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

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
	// copyBudget bounds listing the module's files for the copy.
	copyBudget = time.Minute
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
// search takes every core. The fuzzing runs in a copy of core: go test -fuzz
// writes new corpus entries under testdata/fuzz, which must not land in the
// checkout under test.
//
// Not applicable while core has no Fuzz function: the trigger is Bugboard
// 2865 (add fuzz targets for the gateway's parsers); this test then runs them
// with no change here.
func TestFuzz_targetsSmoke(t *testing.T) {
	t.Parallel()
	realistic.Tool(t, "go", "the fuzz targets run with go test -fuzz")
	s := newScan(t)
	targets := fuzzTargets(t, filepath.Join(s.root, "core"))
	if len(targets) == 0 {
		harness.SkipNotApplicable(t, "core/pkg has no Fuzz functions (func FuzzX(f *testing.F)) yet; "+
			"the smoke run applies once Bugboard 2865 adds them")
	}
	s = scan{f: s.f, root: copyModule(t, s, "core")}
	for _, ft := range targets {
		t.Run(ft.name, func(t *testing.T) {
			res := s.run(t, "core", fuzzBudget, "go", "test", "-run", "^$", "-fuzz", "^"+ft.name+"$", "-fuzztime", fuzzTime, ft.pkg)
			if res.Exit != 0 {
				t.Errorf("%s in %s failed (exit %d); a new failing input is under testdata/fuzz:\n%s", ft.name, ft.pkg, res.Exit, realistic.Tail(res.Output()))
			}
		})
	}
}

// copyModule copies the module dir of the checkout (its tracked and
// untracked, not ignored, files: the source as it stands, no build output)
// into a temporary root and returns that root.
func copyModule(t *testing.T, s scan, dir string) string {
	t.Helper()
	res := s.run(t, dir, copyBudget, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if res.Exit != 0 {
		t.Fatalf("git ls-files in %s failed (exit %d):\n%s", dir, res.Exit, realistic.Tail(res.Output()))
	}
	root := t.TempDir()
	for _, rel := range strings.Split(strings.TrimRight(res.Stdout, "\x00"), "\x00") {
		if err := copyFile(filepath.Join(s.root, dir, rel), filepath.Join(root, dir, rel)); err != nil {
			t.Fatalf("failed to copy %s for fuzzing: %v", dir, err)
		}
	}
	return root
}

// copyFile copies a regular file; a tracked path deleted in the working tree
// is skipped.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("failed to create the directory of %s: %w", dst, err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", dst, err)
	}
	return nil
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
