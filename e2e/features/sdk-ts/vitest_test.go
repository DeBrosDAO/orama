//go:build e2e_fleet

package sdkts

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
)

// vitestConfig is the run's vitest configuration: the SDK's own e2e suite
// and this feature's tests, one file at a time (they share the namespace's
// rate limits), with globals so the feature's files need no vitest import
// (they live outside the SDK and could not resolve one).
const vitestConfig = `export default {
  root: %q,
  test: {
    globals: true,
    environment: "node",
    include: [%q, %q],
    testTimeout: 120000,
    hookTimeout: 120000,
    fileParallelism: false,
  },
};
`

// writeVitestConfig writes the configuration into dir and returns its path.
func writeVitestConfig(t testing.TB, dir, sdkDir, featureTests string) string {
	t.Helper()
	path := filepath.Join(dir, "vitest.e2e-fleet.config.mjs")
	body := fmt.Sprintf(vitestConfig, sdkDir, sdkTestsDir+"/**/*"+testSuffix, filepath.Join(featureTests, "**", "*"+testSuffix))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("failed to write the vitest config: %v", err)
	}
	return path
}

// vitestReport is vitest's --reporter=json output (the Jest-compatible shape).
type vitestReport struct {
	NumTotalTests int          `json:"numTotalTests"`
	TestResults   []fileResult `json:"testResults"`
}

type fileResult struct {
	Name             string      `json:"name"`
	Status           string      `json:"status"`
	Message          string      `json:"message"`
	AssertionResults []assertion `json:"assertionResults"`
}

type assertion struct {
	FullName        string   `json:"fullName"`
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
}

// Vitest assertion states.
const (
	statePassed = "passed"
	stateFailed = "failed"
)

func readReport(t testing.TB, path string) vitestReport {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("vitest wrote no JSON report at %s: %v", path, err)
	}
	var r vitestReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("the vitest report does not decode: %v", err)
	}
	return r
}

var subtestUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// testSuffix names a vitest test file.
const testSuffix = ".test.ts"

// requireEveryFile fails unless every test file under dirs is in the report
// with at least one assertion: a file vitest silently did not pick up (a
// glob that stopped matching, a describe that registers nothing) must not
// pass as "nothing failed".
func requireEveryFile(t *testing.T, r vitestReport, dirs []string) {
	t.Helper()
	counts := map[string]int{}
	for _, file := range r.TestResults {
		counts[filepath.Clean(file.Name)] += len(file.AssertionResults)
	}
	for _, dir := range dirs {
		files := testFiles(t, dir)
		if len(files) == 0 {
			t.Errorf("%s holds no %s file", dir, testSuffix)
		}
		for _, path := range files {
			if counts[path] == 0 {
				t.Errorf("%s: not in the vitest report with an assertion (not collected, or it registers no test)", path)
			}
		}
	}
}

// testFiles lists the test files under dir, recursively.
func testFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, testSuffix) {
			out = append(out, filepath.Clean(path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to list the test files under %s: %v", dir, err)
	}
	return out
}

// replay turns every vitest test into a Go subtest named file/full name, so
// the report counts, fails and skips each SDK test on its own. It returns how
// many assertions failed (a file that did not load counts as one).
func replay(t *testing.T, r vitestReport, redact func(string) string) int {
	t.Helper()
	if len(r.TestResults) == 0 {
		t.Fatal("vitest ran no test file")
	}
	failed := 0
	for _, file := range r.TestResults {
		base := strings.TrimSuffix(filepath.Base(file.Name), testSuffix)
		if len(file.AssertionResults) == 0 && file.Status == stateFailed {
			t.Errorf("%s did not load: %s", file.Name, redact(file.Message))
			failed++
			continue
		}
		for _, a := range file.AssertionResults {
			if a.Status == stateFailed {
				failed++
			}
			name := subtestUnsafe.ReplaceAllString(base+"/"+a.FullName, "_")
			t.Run(name, func(t *testing.T) {
				switch a.Status {
				case statePassed:
				case stateFailed:
					t.Errorf("%s: %s", a.FullName, redact(strings.Join(a.FailureMessages, "\n")))
				default:
					harness.SkipNotApplicable(t, fmt.Sprintf("vitest reported %q for %s (a describe.skipIf whose condition the runner did not satisfy)", a.Status, a.FullName))
				}
			})
		}
	}
	return failed
}
