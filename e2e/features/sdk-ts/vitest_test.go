//go:build e2e_fleet

package sdkts

import (
	"encoding/json"
	"fmt"
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
	body := fmt.Sprintf(vitestConfig, sdkDir, "tests/e2e/**/*.test.ts", filepath.Join(featureTests, "**", "*.test.ts"))
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

// replay turns every vitest test into a Go subtest named file/full name, so
// the report counts, fails and skips each SDK test on its own.
func replay(t *testing.T, r vitestReport, redact func(string) string) {
	t.Helper()
	if len(r.TestResults) == 0 {
		t.Fatal("vitest ran no test file")
	}
	for _, file := range r.TestResults {
		base := strings.TrimSuffix(filepath.Base(file.Name), ".test.ts")
		if len(file.AssertionResults) == 0 && file.Status == stateFailed {
			t.Errorf("%s did not load: %s", file.Name, redact(file.Message))
			continue
		}
		for _, a := range file.AssertionResults {
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
}
