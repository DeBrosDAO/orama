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

func TestVerifySuidDumpable(t *testing.T) {
	read := func(v string, err error) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(v), err }
	}
	if err := verifySuidDumpable(read("0\n", nil)); err != nil {
		t.Fatalf("0: %v", err)
	}
	if err := verifySuidDumpable(read("2\n", nil)); err == nil || !strings.Contains(err.Error(), "is 2") {
		t.Fatalf("2: err = %v, want a refusal naming the value", err)
	}
	if err := verifySuidDumpable(read("", errors.New("permission denied"))); err == nil {
		t.Fatal("an unreadable value was read as off")
	}
}
