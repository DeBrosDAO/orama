package upgrade

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// recorder stands in for every step of an upgrade and records the order they
// ran in.
type recorder struct {
	calls  []string
	fail   string
	update bool
}

func (r *recorder) step(name string) func() error {
	return func() error {
		r.calls = append(r.calls, name)
		if name == r.fail {
			return errors.New(name + " failed")
		}
		return nil
	}
}

func (r *recorder) orchestrator(flags *Flags) *Orchestrator {
	return &Orchestrator{flags: flags, ops: upgradeOps{
		isUpdate:         func() bool { return r.update },
		preferences:      r.step("preferences"),
		prerequisites:    r.step("phase1"),
		provision:        r.step("phase2"),
		torSetup:         r.step("tor"),
		verifyArchive:    r.step("verify-archive"),
		resolvePublicIP:  r.step("public-ip"),
		recordRaftID:     r.step("record-raft-id"),
		handOverAndFence: r.step("hand-over"),
		stopServices:     r.step("stop"),
		portsFree:        r.step("ports-free"),
		installBinaries:  r.step("install-binaries"),
		reexec:           r.step("reexec"),
		secrets:          r.step("phase3"),
		configs:          r.step("phase4"),
		initServices:     r.step("phase2c"),
		torEnsure:        r.step("tor-ensure"),
		privHelper:       r.step("privhelper"),
		removeKeys:       r.step("remove-keys"),
		templates:        r.step("templates"),
		systemdUnits:     r.step("phase5"),
		firewall:         r.step("firewall"),
		restart:          r.step("restart"),
		retireLegacy:     r.step("retire-legacy"),
		clearMaintMode:   r.step("clear-maintenance"),
	}}
}

func indexOf(t *testing.T, calls []string, name string) int {
	t.Helper()
	i := slices.Index(calls, name)
	if i < 0 {
		t.Fatalf("%s never ran: %v", name, calls)
	}
	return i
}

// The whole order of an upgrade of an existing node with --restart.
//
// The hand-over (quorum check, maintenance flag, leadership transfer, another
// leader confirmed) used to run in the restart, after Phase 5 had already
// stopped the node: the leader was stopped without stepping down, and the
// quorum check, run against a half-upgraded node whose rqlite was down or
// unreadable, refused. It belongs before the stop, and so does recording the
// raft identity, which needs the running rqlited.
func TestExecute_order(t *testing.T) {
	r := &recorder{update: true}
	if err := r.orchestrator(&Flags{RestartServices: true}).Execute(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"preferences", "phase1", "phase2", "tor", "verify-archive", "public-ip",
		"record-raft-id", "hand-over",
		"stop", "ports-free", "install-binaries", "reexec",
		"phase3", "phase4", "phase2c", "tor-ensure", "privhelper", "remove-keys", "templates", "phase5", "firewall",
		"restart", "retire-legacy", "clear-maintenance",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Errorf("order:\n got  %v\n want %v", r.calls, want)
	}
}

// Leadership is handed over exactly once, before anything is stopped.
func TestExecute_handsOverBeforeTheStopAndNeverAfter(t *testing.T) {
	r := &recorder{update: true}
	if err := r.orchestrator(&Flags{RestartServices: true}).Execute(); err != nil {
		t.Fatal(err)
	}
	handOver := indexOf(t, r.calls, "hand-over")
	if stop := indexOf(t, r.calls, "stop"); handOver > stop {
		t.Errorf("the hand-over ran after the stop: %v", r.calls)
	}
	if n := len(slices.DeleteFunc(slices.Clone(r.calls), func(c string) bool { return c != "hand-over" })); n != 1 {
		t.Errorf("the hand-over ran %d times", n)
	}
	if raftID := indexOf(t, r.calls, "record-raft-id"); raftID > indexOf(t, r.calls, "stop") {
		t.Errorf("the raft identity was recorded after the stop: %v", r.calls)
	}
}

// A refused hand-over — no quorum after the stop, the leader could not step
// down — leaves the node running: nothing after it happens.
func TestExecute_refusedHandOverStopsNothing(t *testing.T) {
	for _, failing := range []string{"record-raft-id", "hand-over", "public-ip", "verify-archive"} {
		t.Run(failing, func(t *testing.T) {
			r := &recorder{update: true, fail: failing}
			if err := r.orchestrator(&Flags{RestartServices: true}).Execute(); err == nil {
				t.Fatal("the failure was not reported")
			}
			if slices.Contains(r.calls, "stop") {
				t.Errorf("services were stopped after %s failed: %v", failing, r.calls)
			}
		})
	}
}

// A binary swap that cannot hand over to the new binary fails the upgrade: the
// phases after it are the new release's code or nothing.
func TestExecute_failedReexecIsFatal(t *testing.T) {
	r := &recorder{update: true, fail: "reexec"}
	if err := r.orchestrator(&Flags{RestartServices: true}).Execute(); err == nil {
		t.Fatal("a failed re-exec was not reported")
	}
	if slices.Contains(r.calls, "phase3") {
		t.Errorf("the upgrade carried on under the old binary: %v", r.calls)
	}
}

// The maintenance flag is cleared only once the node is back; a restart that
// fails leaves it set, which keeps the node out of rotation.
func TestExecute_failedRestartKeepsTheMaintenanceFlag(t *testing.T) {
	r := &recorder{update: true, fail: "restart"}
	if err := r.orchestrator(&Flags{RestartServices: true}).Execute(); err == nil {
		t.Fatal("a failed restart was not reported")
	}
	if slices.Contains(r.calls, "clear-maintenance") || slices.Contains(r.calls, "retire-legacy") {
		t.Errorf("steps after a failed restart ran: %v", r.calls)
	}
}

// The process resumed after the swap does not repeat the pre-swap steps: the
// services are stopped, there is no leader here to hand over.
func TestExecute_resumedProcessSkipsThePreSwapSteps(t *testing.T) {
	r := &recorder{update: true}
	if err := r.orchestrator(&Flags{RestartServices: true, ReexecedAfterBinarySwap: true}).Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"hand-over", "record-raft-id", "stop", "install-binaries", "reexec"} {
		if slices.Contains(r.calls, c) {
			t.Errorf("%s ran again after the swap: %v", c, r.calls)
		}
	}
	if r.calls[0] != "phase3" {
		t.Errorf("the resumed process started at %s", r.calls[0])
	}
}

// A machine with no installation has nothing to hand over or stop.
func TestExecute_freshMachineStopsNothing(t *testing.T) {
	r := &recorder{update: false}
	if err := r.orchestrator(&Flags{}).Execute(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"hand-over", "record-raft-id", "stop", "restart"} {
		if slices.Contains(r.calls, c) {
			t.Errorf("%s ran on a machine with no installation: %v", c, r.calls)
		}
	}
}

// Without --restart the node is left staged: no restart, and the maintenance
// flag stays until `orama node restart` clears it.
func TestExecute_withoutRestartLeavesTheNodeStaged(t *testing.T) {
	r := &recorder{update: true}
	if err := r.orchestrator(&Flags{}).Execute(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(r.calls, "restart") || slices.Contains(r.calls, "clear-maintenance") {
		t.Errorf("a staged upgrade restarted: %v", r.calls)
	}
}

// The real wiring has every step.
func TestRealOps_everyStepIsWired(t *testing.T) {
	ops := (&Orchestrator{flags: &Flags{}}).realOps()
	v := reflect.ValueOf(ops)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsNil() {
			t.Errorf("upgradeOps.%s is not wired", v.Type().Field(i).Name)
		}
	}
}
