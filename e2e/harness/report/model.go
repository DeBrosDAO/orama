// Package report turns a run — go test -json of every feature package, the
// manifests, the coverage gate, the stage timeline, the recorded evidence and
// the collected artifacts — into report.json, report.junit.xml and a
// self-contained report.html, plus a notification summary and optional
// Bugboard drafts. Output is deterministic for a given input, so it is tested
// against golden files.
package report

import (
	"path"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/artifacts"
	"github.com/DeBrosOfficial/network/e2e/harness/coverage"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

// Verdicts.
const (
	VerdictPass = "PASS"
	// VerdictIncomplete is nothing failed but something was not covered: a
	// skipped test, a feature that never ran, a coverage gap.
	VerdictIncomplete = "INCOMPLETE"
	VerdictFail       = "FAIL"
)

// Feature and test statuses beyond test2json's pass/fail/skip.
const (
	StatusMissing = "missing" // the manifest exists but the package produced no result
)

// MaxEvidencePerFailure is how many of a failed test's last records are shown.
const MaxEvidencePerFailure = 10

// Input is everything a report is built from.
type Input struct {
	RunID     string
	Commit    string
	Manifests []manifest.Manifest
	// Results are the go test -json results of the run (never the re-run).
	Results []gotest.Result
	// Rerun are the targeted re-run results, used only for flakiness labels.
	Rerun     []gotest.Result
	Timeline  *stages.Timeline
	Coverage  *coverage.Result
	Evidence  []evidence.Record
	Artifacts *artifacts.Index
	// RunErrors are failures outside any test: provisioning, teardown, collection.
	RunErrors []string
	// PackageStderr is the tail of each package's go command stderr, keyed
	// by the package run's Output path.
	PackageStderr map[string]string
	// Redactor masks the run's secrets and minted credentials in everything
	// the report shows; nil masks the recognised shapes only.
	Redactor *secrets.Redactor
}

// Report is the built report.
type Report struct {
	RunID          string           `json:"run_id"`
	Commit         string           `json:"commit"`
	Verdict        string           `json:"verdict"`
	VerdictReasons []string         `json:"verdict_reasons"`
	Totals         Totals           `json:"totals"`
	Features       []FeatureResult  `json:"features"`
	Failures       []Failure        `json:"failures"`
	BugsByArea     []AreaBugs       `json:"bugs_by_area"`
	Worked         []string         `json:"worked"`
	Timeline       []StageSummary   `json:"timeline"`
	Coverage       *coverage.Result `json:"coverage,omitempty"`
	FailedArtifact []artifacts.File `json:"failed_artifacts,omitempty"`
	RunErrors      []string         `json:"run_errors,omitempty"`
	Notification   string           `json:"notification"`

	// packageErrs describe every package whose go test failed as a process.
	packageErrs []string
}

// Totals count tests across features.
type Totals struct {
	Features int `json:"features"`
	Tests    int `json:"tests"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
	// NotCovered counts skipped tests and features that never ran.
	NotCovered int `json:"not_covered"`
	Flaky      int `json:"flaky"`
}

// TestResult is one test of a feature.
type TestResult struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Elapsed float64 `json:"elapsed"`
	// Reason is the skip message for a skipped test.
	Reason string `json:"reason,omitempty"`
	// Flakiness is set on failures: deterministic, flaky or unknown.
	Flakiness string `json:"flakiness,omitempty"`
}

// FeatureResult is one feature package.
type FeatureResult struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Area        string       `json:"area"`
	Stage       int          `json:"stage"`
	Destructive bool         `json:"destructive"`
	Status      string       `json:"status"`
	Tests       []TestResult `json:"tests"`
	// PackageOutput is set when the package itself failed (build, TestMain).
	PackageOutput string `json:"package_output,omitempty"`
}

// Failure is one failed test with what explains it.
type Failure struct {
	Feature   string            `json:"feature"`
	Area      string            `json:"area"`
	Test      string            `json:"test"`
	Output    string            `json:"output"`
	Flakiness string            `json:"flakiness"`
	Subtasks  []int             `json:"subtasks,omitempty"`
	Evidence  []evidence.Record `json:"evidence"`
}

// AreaBugs groups failures by manifest area.
type AreaBugs struct {
	Area     string    `json:"area"`
	Failures []Failure `json:"failures"`
}

// StageSummary is one stage on the timeline.
type StageSummary struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Seconds  int64     `json:"seconds"`
	Features []string  `json:"features"`
	Failed   bool      `json:"failed"`
}

// featureOf maps a package import path to its feature id.
func featureOf(pkg string) string { return path.Base(pkg) }

// Build assembles the report. Every text it takes from the run is redacted
// first (redactInput).
func Build(in Input) Report {
	in = redactInput(in)
	r := Report{RunID: in.RunID, Commit: in.Commit, Coverage: in.Coverage, RunErrors: sortedCopy(in.RunErrors)}
	labels := Classify(gotest.Failed(in.Results), in.Rerun)
	byFeature := groupResults(in.Results)
	byID := map[string]manifest.Manifest{}
	for _, m := range sortedManifests(in.Manifests) {
		byID[m.ID] = m
		fr := buildFeature(m, byFeature[m.ID], labels)
		r.Features = append(r.Features, fr)
		r.Failures = append(r.Failures, failuresOf(m, fr, byFeature[m.ID], in.Evidence)...)
	}
	packageFailures(&r, in, byID)
	for _, f := range r.Features {
		if f.Status == gotest.ActionPass {
			r.Worked = append(r.Worked, f.ID+": "+f.Title)
		}
	}
	r.Totals = totals(r.Features)
	r.BugsByArea = groupByArea(r.Failures)
	r.Timeline = timeline(in.Timeline)
	if in.Artifacts != nil {
		r.FailedArtifact = in.Artifacts.Failed()
	}
	r.Verdict, r.VerdictReasons = verdict(r)
	r.Notification = notification(r)
	return r
}

func sortedCopy(ss []string) []string {
	out := append([]string{}, ss...)
	sort.Strings(out)
	return out
}

func sortedManifests(ms []manifest.Manifest) []manifest.Manifest {
	out := append([]manifest.Manifest{}, ms...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Stage != out[j].Stage {
			return out[i].Stage < out[j].Stage
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func groupResults(results []gotest.Result) map[string][]gotest.Result {
	out := map[string][]gotest.Result{}
	for _, res := range results {
		id := featureOf(res.Package)
		out[id] = append(out[id], res)
	}
	return out
}

// skipReason extracts the message t.Skip printed ("    x_test.go:12: <reason>").
func skipReason(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, ".go:"); i > 0 {
			if j := strings.Index(line[i+4:], ": "); j >= 0 {
				return line[i+4+j+2:]
			}
		}
	}
	return ""
}
