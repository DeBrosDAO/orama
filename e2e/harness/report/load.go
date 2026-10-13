package report

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeBrosOfficial/network/e2e/harness/artifacts"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// LoadRun reads a finished run's artifact dir into an Input. The caller adds
// RunID, Commit, Coverage and RunErrors. A missing artifact collection is not
// an error here (a run can fail before it); the report shows it absent.
func LoadRun(artifactDir, featuresDir string) (Input, error) {
	var in Input
	ms, err := manifest.LoadAll(featuresDir)
	if err != nil {
		return in, err
	}
	in.Manifests = ms
	if in.Timeline, err = stages.LoadTimeline(filepath.Join(artifactDir, stages.StateFileName)); err != nil {
		return in, err
	}
	in.PackageStderr = map[string]string{}
	for _, s := range in.Timeline.Stages {
		for _, p := range s.Packages {
			res, err := parseResults(filepath.Join(artifactDir, p.Output))
			if err != nil {
				return in, err
			}
			in.Results = append(in.Results, res...)
			if in.PackageStderr[p.Output], err = readStderrTail(filepath.Join(artifactDir, p.Output+stages.StderrSuffix)); err != nil {
				return in, err
			}
		}
	}
	reruns, err := filepath.Glob(filepath.Join(artifactDir, stages.RerunDir, "*.json"))
	if err != nil {
		return in, fmt.Errorf("failed to list re-runs: %w", err)
	}
	sort.Strings(reruns)
	for _, p := range reruns {
		res, err := parseResults(p)
		if err != nil {
			return in, err
		}
		in.Rerun = append(in.Rerun, res...)
	}
	if in.Evidence, err = loadEvidence(artifactDir, in.Timeline); err != nil {
		return in, err
	}
	ix, err := artifacts.LoadIndex(filepath.Join(artifactDir, artifacts.DirName))
	switch {
	case err == nil:
		in.Artifacts = &ix
	case !errors.Is(err, os.ErrNotExist):
		return in, err
	}
	return in, nil
}

func parseResults(path string) ([]gotest.Result, error) {
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

// readStderrTail is the end of a package's stderr file; missing is empty.
func readStderrTail(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read package stderr %s: %w", path, err)
	}
	return stderrTail(string(raw)), nil
}

// loadEvidence reads each package run's own evidence dir, the one of its
// latest attempt. A package run from before per-run dirs (no Evidence) falls
// back to the shared dir, for its feature only. Re-run evidence is never read.
func loadEvidence(artifactDir string, tl *stages.Timeline) ([]evidence.Record, error) {
	var out []evidence.Record
	shared := map[string]bool{}
	for _, s := range tl.Stages {
		for _, p := range s.Packages {
			if p.Evidence == "" {
				shared[p.Feature] = true
				continue
			}
			recs, err := evidence.Load(filepath.Join(artifactDir, p.Evidence))
			if err != nil {
				return nil, err
			}
			out = append(out, recs...)
		}
	}
	if len(shared) == 0 {
		return out, nil
	}
	recs, err := evidence.Load(filepath.Join(artifactDir, evidence.DirName))
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		if shared[rec.Feature] {
			out = append(out, rec)
		}
	}
	return out, nil
}
