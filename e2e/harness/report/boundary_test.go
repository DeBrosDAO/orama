package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/coverage"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

func smokeOnly() Input {
	return Input{RunID: "r", Commit: "c",
		Manifests: []manifest.Manifest{{ID: "smoke", Title: "Smoke", Area: "platform", Stage: 1}},
		Timeline: &stages.Timeline{Stages: []stages.StageRun{{Stage: stages.Stage{ID: 1, Name: "bootstrap"}, Completed: true,
			Packages: []stages.PackageRun{{Feature: "smoke", Output: "gotest/stage-01-smoke.json"}}}}}}
}

// TestBuild_failedPackageWithPassingTests: go test exited non-zero although
// every parsed test passed (TestMain exit, crash after the last event).
func TestBuild_failedPackageWithPassingTests(t *testing.T) {
	in := smokeOnly()
	in.Results = []gotest.Result{{Package: pkg + "smoke", Test: "TestA_ok", Action: "pass"}, {Package: pkg + "smoke", Action: "pass"}}
	in.Timeline.Stages[0].Packages[0].Exit = 2
	in.PackageStderr = map[string]string{"gotest/stage-01-smoke.json": "panic: evidence file unwritable"}
	r := Build(in)
	if r.Verdict != VerdictFail || len(r.Failures) != 1 || r.Features[0].Status != gotest.ActionFail {
		t.Fatalf("verdict %s failures %+v", r.Verdict, r.Failures)
	}
	if f := r.Failures[0]; f.Test != "(package)" || !strings.Contains(f.Output, "exited 2") || !strings.Contains(f.Output, "evidence file unwritable") {
		t.Fatalf("failure %+v", f)
	}
	if len(r.Worked) != 0 {
		t.Fatalf("a failed package is listed as working: %v", r.Worked)
	}
	in.Timeline.Stages[0].Packages[0].Exit = 0
	in.Timeline.Stages[0].Packages[0].Error = "failed to load the run's redactor"
	if r := Build(in); r.Verdict != VerdictFail {
		t.Fatalf("a runner error did not fail the verdict: %s", r.Verdict)
	}
}

func TestBuild_zeroTestsNotCovered(t *testing.T) {
	in := smokeOnly()
	in.Results = []gotest.Result{{Package: pkg + "smoke", Action: "pass", Output: "testing: warning: no tests to run\n"}}
	r := Build(in)
	if r.Verdict == VerdictPass || r.Totals.NotCovered != 1 || !strings.Contains(strings.Join(r.VerdictReasons, ";"), "executed no test") {
		t.Fatalf("verdict %s totals %+v reasons %v", r.Verdict, r.Totals, r.VerdictReasons)
	}
}

// TestBuild_redactsEveryOutput puts a minted token and credential shapes in
// every text the report takes from a run and checks no output file has them.
func TestBuild_redactsEveryOutput(t *testing.T) {
	const minted = "minted-token-5555555"
	leak := minted + ` {"refresh_token":"rt-4444444"} Authorization: Bearer bb-3333333`
	in := fixture()
	in.Redactor = secrets.NewRedactor(minted)
	in.RunErrors = []string{"teardown: " + leak}
	for i := range in.Results {
		in.Results[i].Output += leak
	}
	in.Evidence = append(in.Evidence, evidence.Record{Kind: "cli", Feature: "auth-siwe", Test: "TestVerify_replay", Seq: 9, Summary: leak, Input: leak, Output: leak, Error: leak})
	in.Timeline.Stages[1].Packages[1].Output = "gotest/stage-03-broken.json"
	in.PackageStderr = map[string]string{"gotest/stage-03-broken.json": leak}
	dir := t.TempDir()
	if err := WriteAll(dir, Build(in), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileJSON, FileJUnit, FileHTML, FileSummary, FileBugs} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{minted, "rt-4444444", "bb-3333333"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("%s contains %s", name, secret)
			}
		}
	}
}

// TestLoadRun_evidenceOfLatestAttemptOnly: a package run reads its own
// evidence dir; stale shared records of the same feature are not mixed in.
func TestLoadRun_evidenceOfLatestAttemptOnly(t *testing.T) {
	run, feats := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(feats, "smoke", "feature.yaml"),
		"id: smoke\ntitle: Smoke\narea: platform\nstage: 1\ncovers:\n  routes: [\"/health\"]\n")
	pr := stages.PackageRun{Feature: "smoke", Output: "gotest/stage-01-smoke.json", Evidence: "evidence/stage-01-smoke", Exit: 1}
	tl := &stages.Timeline{Stages: []stages.StageRun{{Stage: stages.Stage{ID: 1}, Completed: true, Packages: []stages.PackageRun{pr}}}}
	if err := tl.Save(filepath.Join(run, stages.StateFileName)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(run, pr.Output), `{"Action":"fail","Package":"x/features/smoke","Test":"TestA"}`+"\n")
	writeFile(t, filepath.Join(run, pr.Output+stages.StderrSuffix), strings.Repeat("x", 2*MaxStderrTail)+"THE END")
	for dir, summary := range map[string]string{evidence.DirName: "stale", pr.Evidence: "latest"} {
		rec, err := evidence.New(filepath.Join(run, dir), "smoke", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rec.Add(evidence.Record{Kind: evidence.KindHTTP, Test: "TestA", Summary: summary}); err != nil {
			t.Fatal(err)
		}
	}
	in, err := LoadRun(run, feats)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Evidence) != 1 || in.Evidence[0].Summary != "latest" {
		t.Fatalf("evidence %+v", in.Evidence)
	}
	tail := in.PackageStderr[pr.Output]
	if !strings.HasSuffix(tail, "THE END") || len(tail) > MaxStderrTail+len("…") {
		t.Fatalf("stderr tail len %d", len(tail))
	}
}

// TestBuild_skippedSubtestNotCovered: a passing parent with a skipped
// subtest verified nothing for that subtest, so the run is not a PASS.
func TestBuild_skippedSubtestNotCovered(t *testing.T) {
	in := passingSmoke()
	in.Results = []gotest.Result{
		{Package: pkg + "smoke", Test: "TestA", Action: "pass"},
		{Package: pkg + "smoke", Test: "TestA/x", Action: "skip", Output: "    a_test.go:3: not wired\n"},
		{Package: pkg + "smoke", Action: "pass"},
	}
	r := Build(in)
	if r.Verdict == VerdictPass || r.Totals.NotCovered != 1 || r.Features[0].Status != gotest.ActionSkip {
		t.Fatalf("verdict %s totals %+v status %s", r.Verdict, r.Totals, r.Features[0].Status)
	}
}

// TestBuild_ordinaryFailureNotBlamedOnPackage: a failing test also fails
// its package event; the verdict must not call that a package failure.
func TestBuild_ordinaryFailureNotBlamedOnPackage(t *testing.T) {
	in := passingSmoke()
	in.Results = []gotest.Result{
		{Package: pkg + "smoke", Test: "TestA", Action: "fail", Output: "    a_test.go:3: want 1\n"},
		{Package: pkg + "smoke", Action: "fail", Output: "FAIL\n"},
	}
	r := Build(in)
	reasons := strings.Join(r.VerdictReasons, ";")
	if r.Verdict != VerdictFail || strings.Contains(reasons, "package itself failed") {
		t.Fatalf("verdict %s reasons %v", r.Verdict, r.VerdictReasons)
	}
}

// passingSmoke is smokeOnly with a passing coverage gate, so only the
// results decide the verdict.
func passingSmoke() Input {
	in := smokeOnly()
	cov := coverage.Evaluate(nil, in.Manifests, nil)
	in.Coverage = &cov
	return in
}
