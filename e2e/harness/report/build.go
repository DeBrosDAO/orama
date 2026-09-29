package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

func buildFeature(m manifest.Manifest, results []gotest.Result, labels map[string]string) FeatureResult {
	fr := FeatureResult{ID: m.ID, Title: m.Title, Area: m.Area, Stage: m.Stage, Destructive: m.Destructive}
	if len(results) == 0 {
		fr.Status = StatusMissing
		return fr
	}
	pkgFailed := false
	for _, res := range results {
		if res.Test == "" {
			pkgFailed = res.Action == gotest.ActionFail
			fr.PackageOutput = res.Output
			continue
		}
		tr := TestResult{Name: res.Test, Status: res.Action, Elapsed: res.Elapsed}
		switch res.Action {
		case gotest.ActionSkip:
			tr.Reason = skipReason(res.Output)
		case gotest.ActionFail:
			tr.Flakiness = labels[res.Key()]
		}
		fr.Tests = append(fr.Tests, tr)
	}
	if !pkgFailed {
		fr.PackageOutput = ""
	}
	fr.Status = featureStatus(fr.Tests, pkgFailed)
	return fr
}

func featureStatus(tests []TestResult, pkgFailed bool) string {
	status := gotest.ActionPass
	if len(tests) == 0 {
		status = gotest.ActionSkip
	}
	for _, t := range tests {
		switch t.Status {
		case gotest.ActionFail:
			return gotest.ActionFail
		case gotest.ActionSkip:
			status = gotest.ActionSkip
		}
	}
	if pkgFailed {
		return gotest.ActionFail
	}
	return status
}

// failuresOf returns the leaf failures of a feature (a failed subtest, not
// also its failed parent), or the package failure when no test failed.
func failuresOf(m manifest.Manifest, fr FeatureResult, results []gotest.Result, ev []evidence.Record) []Failure {
	failed := map[string]gotest.Result{}
	for _, res := range results {
		if res.Test != "" && res.Action == gotest.ActionFail {
			failed[res.Test] = res
		}
	}
	var out []Failure
	for name, res := range failed {
		if hasFailedChild(name, failed) {
			continue
		}
		out = append(out, Failure{Feature: m.ID, Area: m.Area, Test: name, Output: res.Output,
			Flakiness: flakinessOf(fr, name), Subtasks: m.Subtasks, Evidence: evidenceFor(ev, m.ID, name)})
	}
	if len(out) == 0 && fr.Status == gotest.ActionFail {
		out = append(out, Failure{Feature: m.ID, Area: m.Area, Test: "(package)", Output: fr.PackageOutput,
			Flakiness: FlakinessUnknown, Subtasks: m.Subtasks})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Test < out[j].Test })
	return out
}

func hasFailedChild(name string, failed map[string]gotest.Result) bool {
	for other := range failed {
		if strings.HasPrefix(other, name+"/") {
			return true
		}
	}
	return false
}

func flakinessOf(fr FeatureResult, test string) string {
	for _, t := range fr.Tests {
		if t.Name == test && t.Flakiness != "" {
			return t.Flakiness
		}
	}
	return FlakinessUnknown
}

// evidenceFor returns the last records of test (and of its subtests).
func evidenceFor(ev []evidence.Record, feature, test string) []evidence.Record {
	var out []evidence.Record
	for _, rec := range ev {
		if rec.Feature == feature && (rec.Test == test || strings.HasPrefix(rec.Test, test+"/")) {
			out = append(out, rec)
		}
	}
	if len(out) > MaxEvidencePerFailure {
		out = out[len(out)-MaxEvidencePerFailure:]
	}
	return out
}

// ranNoTest reports whether a feature's package ran and executed no test:
// nothing was verified, so it is not covered.
func ranNoTest(f FeatureResult) bool {
	return f.Status == gotest.ActionSkip && len(f.Tests) == 0
}

func totals(features []FeatureResult) Totals {
	t := Totals{Features: len(features)}
	for _, f := range features {
		if f.Status == StatusMissing || ranNoTest(f) {
			t.NotCovered++
		}
		for _, tr := range f.Tests {
			// A skip at any level verified nothing: a skipped subtest of a
			// passing parent is not covered either.
			if tr.Status == gotest.ActionSkip {
				t.NotCovered++
			}
			if strings.Contains(tr.Name, "/") {
				continue
			}
			t.Tests++
			switch tr.Status {
			case gotest.ActionPass:
				t.Passed++
			case gotest.ActionFail:
				t.Failed++
			}
			if tr.Flakiness == FlakinessFlaky {
				t.Flaky++
			}
		}
	}
	return t
}

// anyTestFailed reports whether a test of f failed: its package then failed
// because of it, not by itself.
func anyTestFailed(f FeatureResult) bool {
	for _, t := range f.Tests {
		if t.Status == gotest.ActionFail {
			return true
		}
	}
	return false
}

func groupByArea(failures []Failure) []AreaBugs {
	by := map[string][]Failure{}
	for _, f := range failures {
		by[f.Area] = append(by[f.Area], f)
	}
	var out []AreaBugs
	for area, fs := range by {
		out = append(out, AreaBugs{Area: area, Failures: fs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Area < out[j].Area })
	return out
}

func timeline(tl *stages.Timeline) []StageSummary {
	if tl == nil {
		return nil
	}
	var out []StageSummary
	for _, s := range tl.Stages {
		sum := StageSummary{ID: s.Stage.ID, Name: s.Stage.Name, Start: s.Start, End: s.End,
			Seconds: int64(s.End.Sub(s.Start).Seconds()), Failed: s.Failed()}
		for _, p := range s.Packages {
			sum.Features = append(sum.Features, p.Feature)
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func verdict(r Report) (string, []string) {
	var fail, incomplete []string
	if r.Totals.Failed > 0 {
		fail = append(fail, fmt.Sprintf("%d test(s) failed (%d flaky: a re-run pass does not clear a failure)", r.Totals.Failed, r.Totals.Flaky))
	}
	for _, f := range r.Features {
		switch {
		case f.Status == gotest.ActionFail && f.PackageOutput != "" && !anyTestFailed(f):
			fail = append(fail, "feature "+f.ID+": the package itself failed (build error, TestMain exit or timeout)")
		case f.Status == StatusMissing:
			incomplete = append(incomplete, "feature "+f.ID+" produced no result")
		case ranNoTest(f):
			incomplete = append(incomplete, "feature "+f.ID+" executed no test")
		}
	}
	for _, e := range r.packageErrs {
		fail = append(fail, "package: "+e)
	}
	for _, e := range r.RunErrors {
		fail = append(fail, "run: "+e)
	}
	if n := r.Totals.NotCovered; n > 0 {
		incomplete = append(incomplete, fmt.Sprintf("%d test(s) or feature(s) not covered (skipped or never ran)", n))
	}
	switch {
	case r.Coverage == nil:
		incomplete = append(incomplete, "coverage gate not evaluated")
	case !r.Coverage.OK():
		incomplete = append(incomplete, fmt.Sprintf("coverage gate: %d uncovered, %d unknown covers, %d stale and %d invalid waivers",
			len(r.Coverage.Uncovered), len(r.Coverage.UnknownCovers), len(r.Coverage.StaleWaivers), len(r.Coverage.InvalidWaivers)))
	}
	switch {
	case len(fail) > 0:
		return VerdictFail, append(fail, incomplete...)
	case len(incomplete) > 0:
		return VerdictIncomplete, incomplete
	}
	return VerdictPass, nil
}

func notification(r Report) string {
	var areas []string
	for _, a := range r.BugsByArea {
		areas = append(areas, fmt.Sprintf("%s(%d)", a.Area, len(a.Failures)))
	}
	s := fmt.Sprintf("Orama e2e %s @ %s: %s — %d passed, %d failed (%d flaky), %d not covered; %d tests in %d features",
		r.RunID, shortCommit(r.Commit), r.Verdict, r.Totals.Passed, r.Totals.Failed, r.Totals.Flaky,
		r.Totals.NotCovered, r.Totals.Tests, r.Totals.Features)
	if len(areas) > 0 {
		s += "; bugs: " + strings.Join(areas, ", ")
	}
	return s
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
