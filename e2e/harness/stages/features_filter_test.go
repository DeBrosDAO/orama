package stages

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

// TestRun_featuresRerunsOnlyThoseAndKeepsTheRest: a rerun of the packages
// that failed replaces their results and keeps every other package's, so a
// one-package fix does not cost a whole stage.
func TestRun_featuresRerunsOnlyThoseAndKeepsTheRest(t *testing.T) {
	fe := &fakeExec{exit: map[string]int{"./features/b": 1}}
	r := newRunner(t, fe)
	steps, err := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "b", Stage: 1}, {ID: "c", Stage: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), steps, Options{}); err != nil {
		t.Fatal(err)
	}
	fe.calls, fe.exit = nil, nil
	tl, err := r.Run(context.Background(), steps, Options{Features: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || !strings.HasSuffix(fe.calls[0][len(fe.calls[0])-1], "/b") {
		t.Fatalf("ran %v, want only ./features/b", fe.calls)
	}
	if len(tl.Stages) != 2 || len(tl.Stages[0].Packages) != 2 {
		t.Fatalf("the timeline lost results: %+v", tl.Stages)
	}
	for _, p := range tl.Stages[0].Packages {
		if p.Exit != 0 {
			t.Errorf("%s exit %d after its rerun passed", p.Feature, p.Exit)
		}
	}
}

func TestRun_featuresOfAStageNeverRunAreAdded(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "c", Stage: 2}})
	tl, err := r.Run(context.Background(), steps, Options{Features: []string{"c"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Stages) != 1 || tl.Stages[0].Stage.ID != 2 || len(tl.Stages[0].Packages) != 1 {
		t.Fatalf("timeline %+v, want stage 2 with c alone", tl.Stages)
	}
}

func TestCheckFeatures(t *testing.T) {
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "kill", Stage: 1, Destructive: true}})
	if err := CheckFeatures(steps, []string{"a", "kill"}); err != nil {
		t.Errorf("known features refused: %v", err)
	}
	if err := CheckFeatures(steps, []string{"a", "nope"}); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown feature: %v, want an error naming it", err)
	}
	if err := CheckFeatures(steps, nil); err != nil {
		t.Errorf("no features: %v", err)
	}
}

// TestRun_concurrentRunnersKeepEachOthersResults: two runners of one artifact
// dir, one rerunning a package while the other reruns another, each keep the
// other's result. Every runner used to save the timeline it loaded at its
// start, so the one that saved last put back the other's stale result (a
// passed rerun reported as failed, stagenet 2026-10-04).
func TestRun_concurrentRunnersKeepEachOthersResults(t *testing.T) {
	first := &fakeExec{exit: map[string]int{"./features/a": 1, "./features/b": 1}}
	r := newRunner(t, first)
	steps, err := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "b", Stage: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), steps, Options{}); err != nil {
		t.Fatal(err)
	}

	other := *r
	other.Exec = (&fakeExec{}).run // b passes on its rerun
	mine := &fakeExec{}            // a passes on its rerun
	r.Exec = func(ctx context.Context, c Command) (int, error) {
		// While a reruns here, the other runner reruns b to completion.
		if _, err := other.Run(ctx, steps, Options{Features: []string{"b"}}); err != nil {
			t.Error(err)
		}
		return mine.run(ctx, c)
	}
	tl, err := r.Run(context.Background(), steps, Options{Features: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range tl.Stages[0].Packages {
		if p.Exit != 0 {
			t.Errorf("%s exit %d: a concurrent runner's passed rerun was lost", p.Feature, p.Exit)
		}
	}
	saved, err := LoadTimeline(filepath.Join(r.ArtifactDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range saved.Stages[0].Packages {
		if p.Exit != 0 {
			t.Errorf("on disk, %s exit %d after both reruns passed", p.Feature, p.Exit)
		}
	}
}

// TestRecord_concurrentSavesKeepEveryResult: saves that race for the state
// file each land, whatever their order.
func TestRecord_concurrentSavesKeepEveryResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFileName)
	const n = 16
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run := StageRun{Stage: Stage{ID: 1}, Packages: []PackageRun{{Feature: fmt.Sprintf("f%02d", i)}}}
			if _, err := record(path, run, true, false); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	tl, err := LoadTimeline(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Stages) != 1 || len(tl.Stages[0].Packages) != n {
		t.Fatalf("%d stages, %d packages on disk after %d concurrent saves, want 1 and %d", len(tl.Stages), len(tl.Stages[0].Packages), n, n)
	}
}
