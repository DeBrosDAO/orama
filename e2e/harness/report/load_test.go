package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/stages"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRun_readsEveryInput(t *testing.T) {
	run, feats := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(feats, "smoke", "feature.yaml"),
		"id: smoke\ntitle: Smoke\narea: platform\nstage: 1\ncovers:\n  routes: [\"/health\"]\n")
	tl := &stages.Timeline{Stages: []stages.StageRun{{Stage: stages.Stage{ID: 1, Name: "bootstrap"}, Completed: true,
		Packages: []stages.PackageRun{{Feature: "smoke", Output: "gotest/stage-01-smoke.json"}}}}}
	if err := tl.Save(filepath.Join(run, stages.StateFileName)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(run, "gotest", "stage-01-smoke.json"),
		`{"Action":"fail","Package":"x/features/smoke","Test":"TestA"}`+"\n")
	writeFile(t, filepath.Join(run, stages.RerunDir, "001-smoke.json"),
		`{"Action":"pass","Package":"x/features/smoke","Test":"TestA"}`+"\n")
	rec, err := evidence.New(filepath.Join(run, evidence.DirName), "smoke", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Add(evidence.Record{Kind: evidence.KindHTTP, Test: "TestA"}); err != nil {
		t.Fatal(err)
	}
	in, err := LoadRun(run, feats)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Manifests) != 1 || len(in.Results) != 1 || len(in.Rerun) != 1 || len(in.Evidence) != 1 || in.Artifacts != nil {
		t.Fatalf("input %+v", in)
	}
	r := Build(in)
	if r.Failures[0].Flakiness != FlakinessFlaky || len(r.Failures[0].Evidence) != 1 {
		t.Fatalf("failure %+v", r.Failures[0])
	}
}

func TestLoadRun_missingOutputFails(t *testing.T) {
	run, feats := t.TempDir(), t.TempDir()
	tl := &stages.Timeline{Stages: []stages.StageRun{{Packages: []stages.PackageRun{{Feature: "x", Output: "gotest/none.json"}}}}}
	if err := tl.Save(filepath.Join(run, stages.StateFileName)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRun(run, feats); err == nil {
		t.Fatal("a timeline pointing at a missing output loaded")
	}
}

func TestLoadRun_emptyRun(t *testing.T) {
	in, err := LoadRun(t.TempDir(), t.TempDir())
	if err != nil || len(in.Results) != 0 || in.Timeline == nil {
		t.Fatalf("in %+v err %v", in, err)
	}
}
