package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/e2e/harness/runctx"
)

// The package's own TestMain is not harness.Main: these tests exercise Main
// by re-running this test binary as a child with a chosen environment.
const childEnv = "E2E_HARNESS_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		Main(m)
		return
	}
	os.Exit(m.Run())
}

func TestChildProbe(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		// Parent run: nothing to probe; the child runs are driven below.
		return
	}
	f := Fleet(t)
	if f.State.RunID != "ab12" {
		t.Fatalf("run id %q", f.State.RunID)
	}
}

// TestChildInterrupt (child only) receives SIGINT mid-run: it must survive it,
// and a test that asks for the fleet afterwards must fail.
func TestChildInterrupt(t *testing.T) {
	if os.Getenv(childEnv) != "1" {
		return
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	// Poll, not Require: the interrupt ends Require's waits (runctx), which
	// is checked below.
	if err := eventually.Poll(context.Background(), 10*time.Millisecond, 10*time.Second, "the interrupt to be noticed", func() (bool, error) {
		return interrupted.Load(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if runctx.Context().Err() == nil {
		t.Error("the interrupt did not cancel the run-wide context")
	}
	t.Run("late", func(t *testing.T) { Fleet(t) })
}

func runChild(t *testing.T, env ...string) (string, int) {
	t.Helper()
	return runChildTest(t, "^TestChildProbe$", env...)
}

func runChildTest(t *testing.T, run string, env ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", run, "-test.v")
	cmd.Env = append(filteredEnv(), append([]string{childEnv + "=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func filteredEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, config.EnvState+"=") || strings.HasPrefix(kv, config.EnvStrict+"=") || strings.HasPrefix(kv, config.EnvEvidenceDir+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func TestMain_strictWithoutStateFails(t *testing.T) {
	out, code := runChild(t, config.EnvStrict+"=1")
	if code != exitRefused || !strings.Contains(out, "refusing to skip") {
		t.Fatalf("code %d out:\n%s", code, out)
	}
}

func TestMain_nonStrictSkips(t *testing.T) {
	out, code := runChild(t)
	if code != 0 || !strings.Contains(out, "not in fleet mode") || !strings.Contains(out, "--- SKIP") {
		t.Fatalf("code %d out:\n%s", code, out)
	}
}

func TestMain_badStrictValue(t *testing.T) {
	_, code := runChild(t, config.EnvStrict+"=perhaps")
	if code != exitBadEnv {
		t.Fatalf("code %d", code)
	}
}

// childState writes a state that passes the run guards, as the provisioner would.
func childState(t *testing.T, mutate func(*fleet.State)) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	st := &fleet.State{RunID: "ab12", Env: "e2e-ab12", BaseDomain: "e2e-ab12.dbrsteting.bid",
		Home: "/tmp/e2e-rw-test0001", RWSock: "/tmp/e2e-rw-test0001/a.sock", ArtifactDir: filepath.Join(dir, "artifacts")}
	if mutate != nil {
		mutate(st)
	}
	path = filepath.Join(dir, "state.json")
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestMain_withStateRunsAndRecords(t *testing.T) {
	dir, path := childState(t, nil)
	out, code := runChild(t, config.EnvState+"="+path, config.EnvStrict+"=1")
	if code != 0 || !strings.Contains(out, "--- PASS: TestChildProbe") {
		t.Fatalf("code %d out:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "artifacts", "evidence")); err != nil {
		t.Fatalf("evidence dir not created: %v", err)
	}
}

// TestMain_refusesUnguardedState: a state aimed at a shared environment is
// refused by every feature package, not only by the runner.
func TestMain_refusesUnguardedState(t *testing.T) {
	_, path := childState(t, func(st *fleet.State) { st.Env = "devnet" })
	out, code := runChild(t, config.EnvState+"="+path, config.EnvStrict+"=1")
	if code != exitRefused || !strings.Contains(out, "devnet") {
		t.Fatalf("code %d out:\n%s", code, out)
	}
}

// TestMain_refusesInvalidPacingBudget: a budget over the product limit would
// let the harness trip the limiter it paces; the package refuses to start.
func TestMain_refusesInvalidPacingBudget(t *testing.T) {
	_, path := childState(t, nil)
	out, code := runChild(t, config.EnvState+"="+path, config.EnvStrict+"=1", pace.EnvCredPerMin+"=31")
	if code != exitRefused || !strings.Contains(out, pace.EnvCredPerMin) {
		t.Fatalf("code %d out:\n%s", code, out)
	}
}

func TestMain_evidenceDirFromRunner(t *testing.T) {
	dir, path := childState(t, nil)
	evDir := filepath.Join(dir, "artifacts", "evidence", "stage-01-harness")
	if _, code := runChild(t, config.EnvState+"="+path, config.EnvEvidenceDir+"="+evDir); code != 0 {
		t.Fatalf("code %d", code)
	}
	if _, err := os.Stat(evDir); err != nil {
		t.Fatalf("the runner's evidence dir was not used: %v", err)
	}
	if _, code := runChild(t, config.EnvState+"="+path, config.EnvEvidenceDir+"=relative/dir"); code != exitRefused {
		t.Fatalf("a relative evidence dir was accepted: %d", code)
	}
}

func TestMain_interruptLetsTestsFinish(t *testing.T) {
	_, path := childState(t, nil)
	out, code := runChildTest(t, "^TestChildInterrupt$", config.EnvState+"="+path, config.EnvStrict+"=1")
	if code != 1 || !strings.Contains(out, InterruptedMessage) || !strings.Contains(out, "--- FAIL: TestChildInterrupt/late") {
		t.Fatalf("code %d out:\n%s", code, out)
	}
}

func TestMain_unreadableState(t *testing.T) {
	_, code := runChild(t, config.EnvState+"="+filepath.Join(t.TempDir(), "missing.json"))
	if code != exitRefused {
		t.Fatalf("code %d", code)
	}
}

func TestSkipNotApplicable_skips(t *testing.T) {
	var inner *testing.T
	t.Run("inner", func(t *testing.T) {
		inner = t
		SkipNotApplicable(t, "x")
		t.Error("SkipNotApplicable returned")
	})
	if !inner.Skipped() || inner.Failed() {
		t.Fatalf("skipped=%v failed=%v", inner.Skipped(), inner.Failed())
	}
}
