package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// MaxStderrTail is how much of a failed package's go command stderr the
// report attaches: the end is where the reason is.
const MaxStderrTail = 4 * 1024

// failedPackage is a timeline package whose go test did not exit 0, or that
// could not be run at all.
type failedPackage struct {
	run   stages.PackageRun
	stage int
}

func failedPackages(tl *stages.Timeline) []failedPackage {
	if tl == nil {
		return nil
	}
	var out []failedPackage
	for _, s := range tl.Stages {
		for _, p := range s.Packages {
			if p.Exit != 0 || p.Error != "" {
				out = append(out, failedPackage{run: p, stage: s.Stage.ID})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].stage != out[j].stage {
			return out[i].stage < out[j].stage
		}
		return out[i].run.Feature < out[j].run.Feature
	})
	return out
}

// describe is one line saying how the package failed.
func (fp failedPackage) describe() string {
	s := fmt.Sprintf("feature %s (stage %d): go test exited %d", fp.run.Feature, fp.stage, fp.run.Exit)
	if fp.run.Error != "" {
		s += ": " + fp.run.Error
	}
	return s
}

// packageFailures adds a "(package)" failure, with the stderr tail, for each
// failed package whose feature has no failure yet: a go test that exited
// non-zero with every parsed test passing (a TestMain exit after the tests,
// a crash after the last event, a runner error) is still a failure.
func packageFailures(r *Report, in Input, ms map[string]manifest.Manifest) {
	failing := map[string]bool{}
	for _, f := range r.Failures {
		failing[f.Feature] = true
	}
	for _, fp := range failedPackages(in.Timeline) {
		r.packageErrs = append(r.packageErrs, fp.describe())
		if failing[fp.run.Feature] {
			continue
		}
		failing[fp.run.Feature] = true
		m := ms[fp.run.Feature]
		out := fp.describe()
		if tail := strings.TrimSpace(in.PackageStderr[fp.run.Output]); tail != "" {
			out += "\n\n[stderr]\n" + tail
		}
		r.Failures = append(r.Failures, Failure{Feature: fp.run.Feature, Area: m.Area, Test: "(package)", Output: out,
			Flakiness: FlakinessUnknown, Subtasks: m.Subtasks})
		for i := range r.Features {
			if r.Features[i].ID == fp.run.Feature {
				r.Features[i].Status = gotest.ActionFail
			}
		}
	}
}

// stderrTail is the last MaxStderrTail bytes of s, on a rune boundary.
func stderrTail(s string) string {
	if len(s) <= MaxStderrTail {
		return s
	}
	cut := len(s) - MaxStderrTail
	for cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut++
	}
	return "…" + s[cut:]
}
