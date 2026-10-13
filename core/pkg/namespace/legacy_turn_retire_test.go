package namespace

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

type fakeLegacyTURN struct {
	hasEnv      bool
	state       systemd.ActiveState
	readErr     error
	teardownErr error
	tornDown    int
}

func (f *fakeLegacyTURN) HasUnitEnv(string, systemd.ServiceType) (bool, error) {
	return f.hasEnv, nil
}
func (f *fakeLegacyTURN) ServiceState(string, systemd.ServiceType) (systemd.ActiveState, error) {
	return f.state, f.readErr
}
func (f *fakeLegacyTURN) TeardownServiceAndEnv(string, systemd.ServiceType) error {
	f.tornDown++
	return f.teardownErr
}

// A legacy unit whose config the migration deleted crash-loops and does not
// read as active; it is retired all the same, env file included, or every
// boot and upgrade starts it again (devnet, bugboard #283 part 2).
func TestRetireLegacyTURNUnit_aCrashLoopingUnitIsRetired(t *testing.T) {
	f := &fakeLegacyTURN{hasEnv: true, state: "activating"}
	retired, err := retireLegacyTURNUnit(f, "anchat")
	if err != nil || !retired || f.tornDown != 1 {
		t.Fatalf("retired=%v err=%v teardowns=%d, want the crash-looping unit retired once", retired, err, f.tornDown)
	}
}

func TestRetireLegacyTURNUnit_aRunningUnitIsRetired(t *testing.T) {
	f := &fakeLegacyTURN{state: "active"}
	if retired, err := retireLegacyTURNUnit(f, "anchat"); err != nil || !retired {
		t.Fatalf("retired=%v err=%v, want a running legacy unit retired", retired, err)
	}
}

// Once retired there is nothing left, and the sweep that runs every minute
// does nothing more.
func TestRetireLegacyTURNUnit_nothingLeftDoesNothing(t *testing.T) {
	f := &fakeLegacyTURN{state: systemd.ActiveStateInactive}
	retired, err := retireLegacyTURNUnit(f, "anchat")
	if err != nil || retired || f.tornDown != 0 {
		t.Fatalf("retired=%v err=%v teardowns=%d, want nothing done", retired, err, f.tornDown)
	}
}

func TestRetireLegacyTURNUnit_aFailedTeardownIsAnError(t *testing.T) {
	f := &fakeLegacyTURN{hasEnv: true, teardownErr: errors.New("disable refused")}
	retired, err := retireLegacyTURNUnit(f, "anchat")
	if err == nil || retired {
		t.Fatalf("retired=%v err=%v, want the failure reported and nothing claimed", retired, err)
	}
}

// The layout migration may already have removed the env file of a unit that
// crash-loops; its failed state still marks it for retirement.
func TestRetireLegacyTURNUnit_aFailedUnitWithoutItsEnvFileIsRetired(t *testing.T) {
	f := &fakeLegacyTURN{state: "failed"}
	if retired, err := retireLegacyTURNUnit(f, "anchat"); err != nil || !retired || f.tornDown != 1 {
		t.Fatalf("retired=%v err=%v teardowns=%d, want the failed unit retired", retired, err, f.tornDown)
	}
}

// A unit whose state cannot be read is left alone, never assumed gone.
func TestRetireLegacyTURNUnit_anUnreadableStateIsAnError(t *testing.T) {
	f := &fakeLegacyTURN{hasEnv: true, readErr: errors.New("systemctl timed out")}
	retired, err := retireLegacyTURNUnit(f, "anchat")
	if err == nil || retired || f.tornDown != 0 {
		t.Fatalf("retired=%v err=%v teardowns=%d, want an error and nothing torn down", retired, err, f.tornDown)
	}
}
