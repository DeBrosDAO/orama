package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/provision"
	"github.com/DeBrosOfficial/network/e2e/harness/report"
)

// fakeProvisioner replaces Up and Down for one test.
func fakeProvisioner(t *testing.T, up func() (*fleet.State, error), downErr error) *[]string {
	t.Helper()
	var downs []string
	oldUp, oldDown := provisionUp, provisionDown
	provisionUp = func(context.Context, provision.Config, provision.Logger) (*fleet.State, error) { return up() }
	provisionDown = func(_ context.Context, st *fleet.State, _ provision.Logger) error {
		downs = append(downs, st.RunID)
		return downErr
	}
	t.Cleanup(func() { provisionUp, provisionDown = oldUp, oldDown })
	return &downs
}

func testRunState(t *testing.T) *runState {
	t.Helper()
	lay, err := findLayout()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	cfg := provision.Config{RunID: "ab12", WorkDir: work, ArtifactDir: filepath.Join(work, "artifacts")}
	return &runState{lay: lay, cfg: cfg, realHome: "/home/o", start: time.Now()}
}

func readReport(t *testing.T, dir string) report.Report {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, report.FileJSON))
	if err != nil {
		t.Fatal(err)
	}
	var r report.Report
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRun_failedTeardownReachesExitCodeAndReport: a teardown that fails after
// the report was written fails the run and is rewritten into the report.
func TestRun_failedTeardownReachesExitCodeAndReport(t *testing.T) {
	downs := fakeProvisioner(t, func() (*fleet.State, error) {
		return nil, &provision.UpError{Err: errors.New("hetzner quota"), Owned: true}
	}, errors.New("2 servers left"))
	rs := testRunState(t)
	code, err := rs.run(context.Background(), context.Background(), false)
	if code != exitFail || err == nil || !strings.Contains(err.Error(), "2 servers left") {
		t.Fatalf("code %d err %v", code, err)
	}
	if len(*downs) != 1 || (*downs)[0] != "ab12" {
		t.Fatalf("downs %v", *downs)
	}
	r := readReport(t, rs.cfg.ArtifactDir)
	joined := strings.Join(r.RunErrors, "\n")
	if r.Verdict != report.VerdictFail || !strings.Contains(joined, "teardown: 2 servers left") || !strings.Contains(joined, "provision: hetzner quota") {
		t.Fatalf("report %s %v", r.Verdict, r.RunErrors)
	}
}

// TestRun_guardFailureNeverKept: a fleet failing the guards is torn down even
// with --keep-on-fail.
func TestRun_guardFailureNeverKept(t *testing.T) {
	bad := &fleet.State{RunID: "ab12", Env: "devnet", ArtifactDir: filepath.Join(t.TempDir(), "a")}
	downs := fakeProvisioner(t, func() (*fleet.State, error) { return bad, nil }, nil)
	rs := testRunState(t)
	code, err := rs.run(context.Background(), context.Background(), true)
	if code != exitFail || err != nil || len(*downs) != 1 {
		t.Fatalf("code %d err %v downs %v", code, err, *downs)
	}
	if r := readReport(t, bad.ArtifactDir); !strings.Contains(strings.Join(r.RunErrors, ""), "post-provision guard") {
		t.Fatalf("run errors %v", r.RunErrors)
	}
}

func TestProvision_upFailureTearsDown(t *testing.T) {
	downs := fakeProvisioner(t, func() (*fleet.State, error) {
		return nil, &provision.UpError{Err: context.Canceled, Owned: true}
	}, errors.New("sweep needed"))
	rs := testRunState(t)
	code, err := rs.provision(context.Background(), context.Background())
	if code != exitFail || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "sweep needed") || len(*downs) != 1 {
		t.Fatalf("code %d err %v downs %v", code, err, *downs)
	}
}

// TestRun_collidingRunIDNeverTornDown: an Up that failed before it owned
// anything (the run id labels another fleet) must not tear down by label.
func TestRun_collidingRunIDNeverTornDown(t *testing.T) {
	collide := errors.New("run id ab12 already has 3 servers")
	downs := fakeProvisioner(t, func() (*fleet.State, error) { return nil, &provision.UpError{Err: collide} }, nil)
	rs := testRunState(t)
	if code, _ := rs.run(context.Background(), context.Background(), false); code != exitFail {
		t.Fatalf("code %d", code)
	}
	if len(*downs) != 0 {
		t.Fatalf("a colliding run id was torn down: %v", *downs)
	}
}

func TestProvision_collidingRunIDNeverTornDown(t *testing.T) {
	downs := fakeProvisioner(t, func() (*fleet.State, error) { return nil, errors.New("preflight: run id in use") }, nil)
	rs := testRunState(t)
	code, err := rs.provision(context.Background(), context.Background())
	if code != exitFail || err == nil || len(*downs) != 0 {
		t.Fatalf("code %d err %v downs %v", code, err, *downs)
	}
}

func TestLoadStateFrom_guardsEveryLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := &fleet.State{RunID: "ab12", Env: "e2e-ab12", BaseDomain: "e2e-ab12.dbrsteting.bid", Home: "/tmp/e2e-rw-test0001", RWSock: "/tmp/e2e-rw-test0001/a.sock"}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadStateFrom(path, "/home/o"); err != nil {
		t.Fatal(err)
	}
	st.BaseDomain = "orama-testnet.network"
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadStateFrom(path, "/home/o"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err %v", err)
	}
	if _, _, err := loadStateFrom(" ", "/home/o"); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestWithSignals_hangupStops(t *testing.T) {
	ctx, stop := withSignals(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	eventually.Require(t, 10*time.Millisecond, 10*time.Second, "SIGHUP to cancel the run", func() (bool, error) {
		return ctx.Err() != nil, nil
	})
}
