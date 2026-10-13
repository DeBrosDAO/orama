package install

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/hardening"
)

type recordedRun struct {
	calls     []string
	loadState string
	failOn    string
}

func (r *recordedRun) run(name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if r.failOn != "" && strings.Contains(call, r.failOn) {
		return []byte("Failed to connect to bus"), errors.New("exit status 1")
	}
	if strings.Contains(call, "LoadState") {
		return []byte(r.loadState + "\n"), nil
	}
	return nil, nil
}

func TestDisableApport_installedIsStoppedAndMasked(t *testing.T) {
	r := &recordedRun{loadState: "loaded"}
	if err := disableApport(r.run); err != nil {
		t.Fatal(err)
	}
	if got := r.calls[len(r.calls)-1]; got != "systemctl mask --now apport.service" {
		t.Fatalf("last call %q, want apport stopped and masked", got)
	}
}

func TestDisableApport_absentOrAlreadyMaskedIsNothingToDo(t *testing.T) {
	for _, state := range []string{"not-found", "masked"} {
		r := &recordedRun{loadState: state}
		if err := disableApport(r.run); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if len(r.calls) != 1 {
			t.Fatalf("%s: calls %v, want only the LoadState read", state, r.calls)
		}
	}
}

func TestDisableApport_unreadableStateIsAnError(t *testing.T) {
	r := &recordedRun{failOn: "LoadState"}
	if err := disableApport(r.run); err == nil {
		t.Fatal("a systemctl that could not answer was read as apport absent")
	}
}

func TestDisableApport_maskFailureIsAnError(t *testing.T) {
	r := &recordedRun{loadState: "loaded", failOn: "mask"}
	err := disableApport(r.run)
	if err == nil || !strings.Contains(err.Error(), "suid core dumps") {
		t.Fatalf("err = %v, want a failure that says why apport must go", err)
	}
}

func TestVerifyRAMHygiene(t *testing.T) {
	live := func(values map[string]string, err error) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) { return []byte(values[p] + "\n"), err }
	}
	good := map[string]string{
		"/proc/sys/fs/suid_dumpable":         "0",
		"/proc/sys/kernel/core_pattern":      "|/bin/false",
		"/proc/sys/kernel/yama/ptrace_scope": "1",
	}
	with := func(path, value string) map[string]string {
		m := map[string]string{}
		for k, v := range good {
			m[k] = v
		}
		m[path] = value
		return m
	}
	if err := verifyRAMHygiene(live(good, nil)); err != nil {
		t.Fatalf("hardened kernel: %v", err)
	}
	dumpable := with("/proc/sys/fs/suid_dumpable", "2")
	if err := verifyRAMHygiene(live(dumpable, nil)); err == nil || !strings.Contains(err.Error(), "suid_dumpable") {
		t.Fatalf("suid_dumpable 2: err = %v, want a refusal naming it", err)
	}
	apportPipe := with("/proc/sys/kernel/core_pattern", "|/usr/share/apport/apport -p%p")
	if err := verifyRAMHygiene(live(apportPipe, nil)); err == nil || !strings.Contains(err.Error(), "core_pattern") {
		t.Fatalf("apport's pipe: err = %v, want a refusal naming core_pattern", err)
	}
	if err := verifyRAMHygiene(live(good, errors.New("permission denied"))); err == nil {
		t.Fatal("an unreadable value was read as hardened")
	}
}

// The drop-in is generated from the same list the read-back and the runtime
// drift check use, so the three cannot disagree.
func TestRAMHygieneSysctl_isTheHardeningList(t *testing.T) {
	set := ramHygieneSettings(t, ramHygieneSysctl)
	for _, v := range hardening.Sysctls {
		if set[v.Key] != v.Want {
			t.Errorf("drop-in sets %s = %q, hardening expects %q", v.Key, set[v.Key], v.Want)
		}
	}
}

func TestDisableSwap_runsSwapoffThenMasksTarget(t *testing.T) {
	r := &recordedRun{}
	if err := disableSwap(r.run); err != nil {
		t.Fatal(err)
	}
	want := []string{"swapoff -a", "systemctl mask swap.target"}
	if strings.Join(r.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", r.calls, want)
	}
}

// swapoff -a exits 0 with no swap configured, so a failure is never "nothing
// to do": it used to be discarded.
func TestDisableSwap_swapoffFailureIsAnActionableError(t *testing.T) {
	r := &recordedRun{failOn: "swapoff"}
	err := disableSwap(r.run)
	if err == nil || !strings.Contains(err.Error(), "/proc/swaps") || !strings.Contains(err.Error(), "Failed to connect") {
		t.Fatalf("err = %v, want a failure that names where to look and carries the command output", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls %v: the target was masked after swap stayed on", r.calls)
	}
}

func TestDisableSwap_maskFailureIsAnError(t *testing.T) {
	r := &recordedRun{failOn: "mask swap.target"}
	if err := disableSwap(r.run); err == nil || !strings.Contains(err.Error(), "swap.target") {
		t.Fatalf("err = %v, want a failure naming swap.target", err)
	}
}
