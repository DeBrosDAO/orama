package install

import (
	"errors"
	"strings"
	"testing"
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
	good := map[string]string{"/proc/sys/fs/suid_dumpable": "0", "/proc/sys/kernel/core_pattern": "|/bin/false"}
	if err := verifyRAMHygiene(live(good, nil)); err != nil {
		t.Fatalf("hardened kernel: %v", err)
	}
	dumpable := map[string]string{"/proc/sys/fs/suid_dumpable": "2", "/proc/sys/kernel/core_pattern": "|/bin/false"}
	if err := verifyRAMHygiene(live(dumpable, nil)); err == nil || !strings.Contains(err.Error(), "suid_dumpable") {
		t.Fatalf("suid_dumpable 2: err = %v, want a refusal naming it", err)
	}
	apportPipe := map[string]string{"/proc/sys/fs/suid_dumpable": "0", "/proc/sys/kernel/core_pattern": "|/usr/share/apport/apport -p%p"}
	if err := verifyRAMHygiene(live(apportPipe, nil)); err == nil || !strings.Contains(err.Error(), "core_pattern") {
		t.Fatalf("apport's pipe: err = %v, want a refusal naming core_pattern", err)
	}
	if err := verifyRAMHygiene(live(good, errors.New("permission denied"))); err == nil {
		t.Fatal("an unreadable value was read as hardened")
	}
}

// The read-back checks what the drop-in sets, value for value.
func TestRAMHygieneLive_matchesTheDropIn(t *testing.T) {
	set := ramHygieneSettings(t, ramHygieneSysctl)
	for _, v := range ramHygieneLive {
		key := strings.ReplaceAll(strings.TrimPrefix(v.path, "/proc/sys/"), "/", ".")
		if set[key] != v.want {
			t.Errorf("read-back expects %s = %q, the drop-in sets %q", key, v.want, set[key])
		}
	}
}
