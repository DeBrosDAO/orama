package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/artifacts"
	"github.com/DeBrosOfficial/network/e2e/harness/coverage"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/gotest"
	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const pkg = "github.com/DeBrosOfficial/network/e2e/features/"

func fixture() Input {
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	ms := []manifest.Manifest{
		{ID: "smoke", Title: "Smoke", Area: "platform", Stage: 1, Covers: manifest.Covers{Routes: []string{"/health"}}},
		{ID: "auth-siwe", Title: "SIWE sign-in", Area: "auth", Stage: 3, Subtasks: []int{2830}, Covers: manifest.Covers{Routes: []string{"/v1/auth/verify"}}},
		{ID: "kill-voter", Title: "Kill a voter", Area: "ops", Stage: 9, Destructive: true, Covers: manifest.Covers{CLI: []string{"orama node remove"}}},
		{ID: "broken", Title: "Broken package", Area: "auth", Stage: 3, Covers: manifest.Covers{Routes: []string{"/v1/auth/logout"}}},
	}
	results := []gotest.Result{
		{Package: pkg + "smoke", Test: "TestHealth_ok", Action: "pass", Elapsed: 0.4},
		{Package: pkg + "smoke", Action: "pass", Elapsed: 1},
		{Package: pkg + "auth-siwe", Test: "TestVerify_replay", Action: "fail", Elapsed: 2, Output: "    verify_test.go:40: want 401, got 200\n"},
		{Package: pkg + "auth-siwe", Test: "TestVerify_devices", Action: "fail", Elapsed: 3},
		{Package: pkg + "auth-siwe", Test: "TestVerify_devices/es256", Action: "fail", Elapsed: 1, Output: "    device_test.go:9: DER refused\n"},
		{Package: pkg + "auth-siwe", Test: "TestVerify_solana", Action: "skip", Output: "    solana_test.go:12: not applicable: SIWS disabled on this cluster\n"},
		{Package: pkg + "auth-siwe", Action: "fail", Elapsed: 6},
		{Package: pkg + "broken", Action: "fail", Output: "features/broken/x_test.go:3:1: syntax error\n"},
	}
	rerun := []gotest.Result{
		{Package: pkg + "auth-siwe", Test: "TestVerify_replay", Action: "pass"},
		{Package: pkg + "auth-siwe", Test: "TestVerify_devices/es256", Action: "fail"},
	}
	tl := &stages.Timeline{Stages: []stages.StageRun{
		{Stage: stages.Stage{ID: 1, Name: "bootstrap"}, Start: t0, End: t0.Add(90 * time.Second), Completed: true,
			Packages: []stages.PackageRun{{Feature: "smoke"}}},
		{Stage: stages.Stage{ID: 3, Name: "auth"}, Start: t0.Add(2 * time.Minute), End: t0.Add(5 * time.Minute), Completed: true,
			Packages: []stages.PackageRun{{Feature: "auth-siwe", Exit: 1}, {Feature: "broken", Exit: 1}}},
	}}
	ev := []evidence.Record{
		{Kind: "http", Feature: "auth-siwe", Test: "TestVerify_replay", Seq: 1, Summary: "POST https://e2e/v1/auth/verify", Status: 200, Input: "{\"message\":\"m\"}", Output: "{\"access_token\":\"[REDACTED]\"}"},
		{Kind: "cli", Feature: "auth-siwe", Test: "TestVerify_devices/es256", Seq: 2, Summary: "orama auth login", Status: 1, Output: "<refused>"},
		{Kind: "ssh", Feature: "smoke", Test: "TestHealth_ok", Seq: 1, Summary: "node-1: true"},
	}
	cov := coverage.Evaluate(
		[]coverage.Item{{ID: "route:/health", Kind: "route"}, {ID: "route:/v1/auth/verify", Kind: "route"}, {ID: "route:/v1/x", Kind: "route"}, {ID: "cli:orama node remove", Kind: "cli"}, {ID: "route:/v1/auth/logout", Kind: "route"}},
		ms, []coverage.Waiver{{ID: "route:/v1/x", Reason: "needs RootWallet orama-tx", Trigger: "task 2857"}})
	return Input{RunID: "e2e-ab12", Commit: "0123456789abcdef", Manifests: ms, Results: results, Rerun: rerun,
		Timeline: tl, Coverage: &cov, Evidence: ev,
		Artifacts: &artifacts.Index{Files: []artifacts.File{{Path: "nodes/node-2/wireguard.txt", Source: "node-2", Command: "wg show all", Error: "ssh: handshake failed"}}},
		RunErrors: []string{"teardown: 1 server left, swept by label"}}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./harness/report -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run with -update after reviewing", name)
	}
}

func TestWriteAll_golden(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAll(dir, Build(fixture()), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileJSON, FileJUnit, FileHTML, FileSummary, FileBugs} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, name+".golden", got)
	}
}

func TestBuild_deterministic(t *testing.T) {
	a, _ := json.Marshal(Build(fixture()))
	b, _ := json.Marshal(Build(fixture()))
	if !bytes.Equal(a, b) {
		t.Fatal("two builds of the same input differ")
	}
}

func TestBuild_verdictAndLabels(t *testing.T) {
	r := Build(fixture())
	if r.Verdict != VerdictFail {
		t.Fatalf("verdict %s", r.Verdict)
	}
	if r.Totals.Failed != 2 || r.Totals.Flaky != 1 || r.Totals.Passed != 1 || r.Totals.NotCovered != 2 {
		t.Fatalf("totals %+v", r.Totals)
	}
	labels := map[string]string{}
	for _, f := range r.Failures {
		labels[f.Test] = f.Flakiness
	}
	if labels["TestVerify_replay"] != FlakinessFlaky || labels["TestVerify_devices/es256"] != FlakinessDeterministic || labels["(package)"] != FlakinessUnknown {
		t.Fatalf("labels %v", labels)
	}
	if _, parent := labels["TestVerify_devices"]; parent {
		t.Fatal("a parent of a failed subtest is listed as its own failure")
	}
	if len(r.Worked) != 1 || !strings.HasPrefix(r.Worked[0], "smoke") {
		t.Fatalf("worked %v", r.Worked)
	}
}

func TestBuild_flakyRerunNeverPasses(t *testing.T) {
	in := fixture()
	in.RunErrors = nil
	in.Results = []gotest.Result{{Package: pkg + "smoke", Test: "TestA", Action: "fail"}}
	in.Rerun = []gotest.Result{{Package: pkg + "smoke", Test: "TestA", Action: "pass"}}
	in.Manifests = in.Manifests[:1]
	r := Build(in)
	if r.Verdict != VerdictFail || r.Totals.Failed != 1 {
		t.Fatalf("a flaky failure changed the verdict: %s %+v", r.Verdict, r.Totals)
	}
}

func TestBuild_passAndIncomplete(t *testing.T) {
	in := fixture()
	in.RunErrors = nil
	in.Manifests = in.Manifests[:1]
	in.Timeline = &stages.Timeline{Stages: in.Timeline.Stages[:1]}
	in.Results = []gotest.Result{{Package: pkg + "smoke", Test: "TestA", Action: "pass"}, {Package: pkg + "smoke", Action: "pass"}}
	cov := coverage.Evaluate([]coverage.Item{{ID: "route:/health", Kind: "route"}}, in.Manifests, nil)
	in.Coverage = &cov
	if r := Build(in); r.Verdict != VerdictPass || len(r.VerdictReasons) != 0 {
		t.Fatalf("verdict %s %v", r.Verdict, r.VerdictReasons)
	}
	in.Coverage = nil
	if r := Build(in); r.Verdict != VerdictIncomplete {
		t.Fatalf("no coverage: verdict %s", r.Verdict)
	}
	in.Coverage = &cov
	in.Results = nil
	if r := Build(in); r.Verdict != VerdictIncomplete || r.Features[0].Status != StatusMissing {
		t.Fatalf("missing feature: %s %+v", r.Verdict, r.Features)
	}
}

func TestBuild_emptyInput(t *testing.T) {
	r := Build(Input{})
	if r.Verdict != VerdictIncomplete || r.Totals.Tests != 0 {
		t.Fatalf("empty: %s %+v", r.Verdict, r.Totals)
	}
	if _, err := HTML(r); err != nil {
		t.Fatal(err)
	}
	if _, err := JUnit(r); err != nil {
		t.Fatal(err)
	}
}

func TestClassify_labels(t *testing.T) {
	failed := []gotest.Result{{Package: "p", Test: "A"}, {Package: "p", Test: "B"}, {Package: "p", Test: "C"}, {Package: "p"}}
	rerun := []gotest.Result{{Package: "p", Test: "A", Action: "pass"}, {Package: "p", Test: "B", Action: "fail"}, {Package: "p", Test: "C", Action: "skip"}}
	got := Classify(failed, rerun)
	if got["p A"] != FlakinessFlaky || got["p B"] != FlakinessDeterministic || got["p C"] != FlakinessUnknown || got["p "] != FlakinessUnknown {
		t.Fatalf("labels %v", got)
	}
}

func TestHTML_selfContained(t *testing.T) {
	html, err := HTML(Build(fixture()))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"<script", "http://", "<link", "@import"} {
		if bytes.Contains(html, []byte(banned)) {
			t.Errorf("report.html contains %q", banned)
		}
	}
	if !bytes.Contains(html, []byte("&lt;refused&gt;")) {
		t.Error("evidence is not HTML-escaped")
	}
}
