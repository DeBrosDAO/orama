package stages

import (
	"context"
	"strings"
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
