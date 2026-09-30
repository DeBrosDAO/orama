package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
)

// RerunDir holds the targeted re-runs, apart from the runs the verdict uses.
const RerunDir = "rerun"

// FailedTest names one failed test for a targeted re-run.
type FailedTest struct {
	Feature string
	Test    string
}

// Rerun runs each failed test once more, alone, and returns the re-run results.
// It exists only to label a failure deterministic or flaky; the verdict is
// computed from the original run and a re-run pass never changes it.
func (r *Runner) Rerun(ctx context.Context, failed []FailedTest, timeout Duration) ([]gotest.Result, error) {
	if err := os.MkdirAll(filepath.Join(r.ArtifactDir, RerunDir), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create re-run dir: %w", err)
	}
	var out []gotest.Result
	for i, f := range failed {
		if f.Test == "" {
			// A package-level failure (build error, TestMain exit) is not a
			// test that can be run alone.
			continue
		}
		name := fmt.Sprintf("%03d-%s", i+1, f.Feature)
		rel := filepath.Join(RerunDir, name+".json")
		args := append(r.goTestArgs(binaryTimeout(timeout)), "-run", "^"+regexp.QuoteMeta(f.Test)+"$",
			"./features/"+f.Feature)
		if _, msg := r.execTo(ctx, rel, filepath.Join(RerunDir, evidence.DirName, name), args, time.Duration(timeout)); msg != "" {
			return out, fmt.Errorf("failed to re-run %s %s: %s", f.Feature, f.Test, msg)
		}
		results, err := parseFile(filepath.Join(r.ArtifactDir, rel))
		if err != nil {
			return out, err
		}
		out = append(out, results...)
	}
	return out, nil
}

func parseFile(path string) ([]gotest.Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open go test output %s: %w", path, err)
	}
	defer f.Close()
	res, err := gotest.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return res, nil
}
