package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/manifest"
)

func TestLoad_realStagesFile(t *testing.T) {
	st, err := Load(filepath.Join("..", "..", FilePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != manifest.MaxStage || st[0].Name != "bootstrap" || st[10].Name != "chaos-soak" {
		t.Fatalf("stages %+v", st)
	}
}

func writeStages(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_invalid(t *testing.T) {
	var ok strings.Builder
	ok.WriteString("stages:\n")
	for i := 1; i <= manifest.MaxStage; i++ {
		fmt.Fprintf(&ok, "  - {id: %d, name: s%d, timeout: 1m}\n", i, i)
	}
	if _, err := Load(writeStages(t, ok.String())); err != nil {
		t.Fatalf("valid file refused: %v", err)
	}
	cases := map[string]string{
		"too few":      "stages:\n  - {id: 1, name: a, timeout: 1m}\n",
		"bad duration": strings.Replace(ok.String(), "timeout: 1m}\n", "timeout: soon}\n", 1),
		"out of order": strings.Replace(ok.String(), "{id: 2,", "{id: 3,", 1),
		"dup name":     strings.Replace(ok.String(), "name: s2,", "name: s1,", 1),
		"zero timeout": strings.Replace(ok.String(), "timeout: 1m}\n", "timeout: 0s}\n", 1),
		"unknown key":  strings.Replace(ok.String(), "timeout: 1m}\n", "timeout: 1m, parallel: 2}\n", 1),
	}
	for name, body := range cases {
		if _, err := Load(writeStages(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func testStages() []Stage {
	return []Stage{{ID: 1, Name: "bootstrap", Timeout: Duration(time.Minute)}, {ID: 2, Name: "auth", Timeout: Duration(time.Minute)}}
}

func TestPlan_splitsDestructive(t *testing.T) {
	steps, err := Plan(testStages(), []manifest.Manifest{
		{ID: "b", Stage: 1}, {ID: "a", Stage: 1}, {ID: "kill", Stage: 1, Destructive: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps[0].Parallel, ",") != "a,b" || strings.Join(steps[0].Destructive, ",") != "kill" {
		t.Fatalf("step %+v", steps[0])
	}
	if len(steps[1].Features()) != 0 {
		t.Fatalf("empty stage has features: %+v", steps[1])
	}
	if strings.Join(steps[0].Features(), ",") != "a,b,kill" {
		t.Fatalf("features %v", steps[0].Features())
	}
	if _, err := Plan(testStages(), []manifest.Manifest{{ID: "x", Stage: 7}}); err == nil {
		t.Fatal("unknown stage accepted")
	}
}

type fakeExec struct {
	mu      sync.Mutex
	calls   [][]string
	envs    [][]string
	budgets []time.Duration
	exit    map[string]int
}

func (f *fakeExec) run(_ context.Context, c Command) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.Args)
	f.envs = append(f.envs, c.Env)
	f.budgets = append(f.budgets, c.Budget)
	f.mu.Unlock()
	pkg := c.Args[len(c.Args)-1]
	fmt.Fprintf(c.Stdout, `{"Action":"run","Package":"%s","Test":"TestX"}`+"\n", pkg)
	code := f.exit[pkg]
	action := "pass"
	if code != 0 {
		action = "fail"
	}
	fmt.Fprintf(c.Stdout, `{"Action":"%s","Package":"%s","Test":"TestX"}`+"\n", action, pkg)
	return code, nil
}

func newRunner(t *testing.T, fe *fakeExec) *Runner {
	return &Runner{ModuleDir: "/e2e", StatePath: "/run/state.json", ArtifactDir: t.TempDir(),
		BaseEnv: []string{"PATH=/bin"}, Exec: fe.run,
		Now: func() time.Time { return time.Unix(0, 0).UTC() }, Logf: t.Logf}
}

func TestRun_allStagesDestructiveLastAndEnv(t *testing.T) {
	fe := &fakeExec{exit: map[string]int{"./features/b": 1}}
	r := newRunner(t, fe)
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "b", Stage: 1}, {ID: "kill", Stage: 1, Destructive: true}, {ID: "c", Stage: 2}})
	tl, err := r.Run(context.Background(), steps, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 4 || fe.calls[2][len(fe.calls[2])-1] != "./features/kill" {
		t.Fatalf("calls %v", fe.calls)
	}
	if !tl.Stages[0].Failed() || tl.Stages[1].Failed() || !tl.Stages[0].Completed {
		t.Fatalf("timeline %+v", tl)
	}
	env := strings.Join(fe.envs[0], " ")
	if !strings.Contains(env, "E2E_FLEET_STATE=/run/state.json") || !strings.Contains(env, "E2E_STRICT=1") ||
		!strings.Contains(env, "E2E_EVIDENCE_DIR="+filepath.Join(r.ArtifactDir, "evidence", "stage-01-")) {
		t.Fatalf("env %s", env)
	}
	args := strings.Join(fe.calls[0], " ")
	// The binary's own timeout (which runs no cleanup) sits a grace period
	// past the stage budget the executor enforces with an interrupt.
	if !strings.Contains(args, "-tags e2e_fleet -json -count=1 -timeout 11m0s") {
		t.Fatalf("args %s", args)
	}
	if fe.budgets[0] != time.Minute {
		t.Fatalf("budget %v, want the stage timeout", fe.budgets[0])
	}
	if _, err := os.Stat(filepath.Join(r.ArtifactDir, GoTestDir, "stage-01-a.json")); err != nil {
		t.Fatalf("output file: %v", err)
	}
}

func TestRun_onlyAndResume(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "c", Stage: 2}})
	if _, err := r.Run(context.Background(), steps, Options{Only: 1}); err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 {
		t.Fatalf("only=1 ran %v", fe.calls)
	}
	tl, err := r.Run(context.Background(), steps, Options{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 2 || fe.calls[1][len(fe.calls[1])-1] != "./features/c" {
		t.Fatalf("resume re-ran a completed stage: %v", fe.calls)
	}
	if len(tl.Stages) != 2 || !tl.Completed(1) || !tl.Completed(2) {
		t.Fatalf("timeline %+v", tl)
	}
}

func TestRun_cancelledContext(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	steps, _ := Plan(testStages(), nil)
	if _, err := r.Run(ctx, steps, Options{}); err == nil {
		t.Fatal("cancelled run returned no error")
	}
}

func TestRun_executorErrorRecorded(t *testing.T) {
	r := newRunner(t, &fakeExec{})
	r.Exec = func(context.Context, Command) (int, error) { return -1, fmt.Errorf("go not found") }
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}})
	tl, err := r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p := tl.Stages[0].Packages[0]; p.Error == "" || !tl.Stages[0].Failed() {
		t.Fatalf("package %+v", p)
	}
}

func TestRerun_labelsOnlyTests(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	res, err := r.Rerun(context.Background(), []FailedTest{{Feature: "a", Test: "TestX"}, {Feature: "b"}}, Duration(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(fe.calls) != 1 || !strings.Contains(strings.Join(fe.calls[0], " "), "-run ^TestX$") {
		t.Fatalf("calls %v", fe.calls)
	}
	if len(res) == 0 || res[0].Action != "pass" {
		t.Fatalf("results %+v", res)
	}
}

func TestLoadTimeline_missingAndCorrupt(t *testing.T) {
	tl, err := LoadTimeline(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || len(tl.Stages) != 0 {
		t.Fatalf("missing: %+v %v", tl, err)
	}
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTimeline(p); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

// TestRun_onlyKeepsOtherStages: `test --stage 2` after a full run replaces
// stage 2 and keeps stage 1's results in the timeline.
func TestRun_onlyKeepsOtherStages(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "c", Stage: 2}})
	if _, err := r.Run(context.Background(), steps, Options{}); err != nil {
		t.Fatal(err)
	}
	fe.exit = map[string]int{"./features/c": 1}
	tl, err := r.Run(context.Background(), steps, Options{Only: 2})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := LoadTimeline(filepath.Join(r.ArtifactDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []*Timeline{tl, saved} {
		if len(got.Stages) != 2 || !got.Completed(1) || !got.Stages[1].Failed() {
			t.Fatalf("timeline %+v", got)
		}
	}
}

// TestRun_restoresNodesAfterEachDestructivePackage: the hook runs after
// every destructive package (never after a parallel one), even on a
// cancelled run, and its failure fails that package.
func TestRun_restoresNodesAfterEachDestructivePackage(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	var calls int
	r.AfterDestructive = func(ctx context.Context) error {
		calls++
		if ctx.Err() != nil {
			t.Error("the restore ran on a cancelled context")
		}
		return fmt.Errorf("iptables still holds rules")
	}
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "k1", Stage: 1, Destructive: true}, {ID: "k2", Stage: 1, Destructive: true}})
	tl, err := r.Run(context.Background(), steps, Options{Only: 1})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("restored %d times, want 2", calls)
	}
	for _, p := range tl.Stages[0].Packages {
		if strings.HasPrefix(p.Feature, "k") != strings.Contains(p.Error, "still holds rules") {
			t.Fatalf("package %s error %q", p.Feature, p.Error)
		}
	}
}

func TestWorstCase_parallelOnceDestructiveEach(t *testing.T) {
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}, {ID: "b", Stage: 1},
		{ID: "k1", Stage: 1, Destructive: true}, {ID: "k2", Stage: 2, Destructive: true}})
	// Each slot can run its stage timeout plus StopGrace before the runner kills it.
	if got, want := WorstCase(steps), 3*time.Minute+3*StopGrace; got != want {
		t.Fatalf("worst case %s, want %s (stage 1: parallel + k1; stage 2: k2, each with the stop grace)", got, want)
	}
	if got := WorstCase(nil); got != 0 {
		t.Fatalf("no steps: %s", got)
	}
}

func TestRun_prefixWrapsEveryPackage(t *testing.T) {
	fe := &fakeExec{}
	r := newRunner(t, fe)
	r.Prefix = []string{"bwrap", "--"}
	steps, _ := Plan(testStages(), []manifest.Manifest{{ID: "a", Stage: 1}})
	if _, err := r.Run(context.Background(), steps, Options{Only: 1}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fe.calls[0][:4], " "); got != "bwrap -- go test" {
		t.Fatalf("args %v", fe.calls[0])
	}
}
