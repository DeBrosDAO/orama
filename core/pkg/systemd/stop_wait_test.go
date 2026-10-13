package systemd

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// failStop makes `systemctl stop` fail the way it does when the stop job is
// cancelled or the command gives up before the job finishes.
func failStop(m *Manager) {
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		if args[0] == "stop" {
			return []byte("Job for unit canceled."), errors.New("exit status 1")
		}
		return nil, nil
	}
}

// A unit still deactivating when the stop command returns is waited for: the
// teardown that follows deletes what it runs from, and the SFU drains for 30s.
func TestStopUnit_waitsForADeactivatingUnitToFinishStopping(t *testing.T) {
	m, _ := newFakeManager(t)
	failStop(m)
	m.stopPollInterval = time.Millisecond
	reads := 0
	m.unitState = func(string) (unitState, error) {
		reads++
		if reads < 4 {
			return unitState{Load: "loaded", Active: "deactivating"}, nil
		}
		return unitState{Load: "loaded", Active: "inactive"}, nil
	}

	if err := m.StopService("acme", ServiceTypeSFU); err != nil {
		t.Fatalf("StopService: %v", err)
	}
	if reads < 4 {
		t.Errorf("state read %d times; the unit was not waited for", reads)
	}
}

func TestStopUnit_reportsAUnitStillDeactivatingAtTheDeadline(t *testing.T) {
	m, _ := newFakeManager(t)
	failStop(m)
	m.stopPollInterval = time.Millisecond
	m.stopWaitDeadline = 20 * time.Millisecond
	m.unitState = func(string) (unitState, error) {
		return unitState{Load: "loaded", Active: "deactivating"}, nil
	}

	err := m.StopService("acme", ServiceTypeSFU)
	if err == nil || !strings.Contains(err.Error(), "still deactivating") {
		t.Fatalf("err = %v, want the unit reported still deactivating", err)
	}
}

// A unit that went back up after the wait (its stop was cancelled by a start)
// is a failed stop, not a success.
func TestStopUnit_aUnitThatIsActiveAfterTheWaitIsAFailedStop(t *testing.T) {
	m, _ := newFakeManager(t)
	failStop(m)
	m.stopPollInterval = time.Millisecond
	reads := 0
	m.unitState = func(string) (unitState, error) {
		reads++
		if reads < 2 {
			return unitState{Load: "loaded", Active: "deactivating"}, nil
		}
		return unitState{Load: "loaded", Active: "active"}, nil
	}

	err := m.StopService("acme", ServiceTypeSFU)
	if err == nil || !strings.Contains(err.Error(), "still active") {
		t.Fatalf("err = %v, want a failed stop (still active)", err)
	}
}

func TestStopUnit_anUnreadableStateWhileWaitingIsAnError(t *testing.T) {
	m, _ := newFakeManager(t)
	failStop(m)
	m.stopPollInterval = time.Millisecond
	reads := 0
	m.unitState = func(string) (unitState, error) {
		reads++
		if reads == 1 {
			return unitState{Load: "loaded", Active: "deactivating"}, nil
		}
		return unitState{}, errors.New("systemctl show failed")
	}

	if err := m.StopService("acme", ServiceTypeSFU); err == nil {
		t.Fatal("an unreadable state was treated as a stopped unit")
	}
}

func TestServiceState_reportsTheRealState(t *testing.T) {
	for _, tc := range []struct {
		active       string
		running      bool
		transitional bool
	}{
		{"active", true, false},
		{"activating", false, true},
		{"deactivating", false, true},
		{"reloading", false, true},
		{"inactive", false, false},
		{"failed", false, false},
	} {
		t.Run(tc.active, func(t *testing.T) {
			m, _ := newFakeManager(t)
			m.unitState = func(unit string) (unitState, error) {
				if unit != "orama-namespace-sfu@acme.service" {
					t.Errorf("asked about %q", unit)
				}
				return unitState{Load: "loaded", Active: tc.active}, nil
			}
			got, err := m.ServiceState("acme", ServiceTypeSFU)
			if err != nil {
				t.Fatal(err)
			}
			if got.Running() != tc.running || got.Transitional() != tc.transitional {
				t.Errorf("state %q: running=%v transitional=%v, want %v/%v", got, got.Running(), got.Transitional(), tc.running, tc.transitional)
			}
		})
	}
}

func TestServiceState_anUnreadableStateIsAnError(t *testing.T) {
	m, _ := newFakeManager(t)
	m.unitState = func(string) (unitState, error) { return unitState{}, errors.New("systemctl show failed") }
	if _, err := m.ServiceState("acme", ServiceTypeSFU); err == nil {
		t.Fatal("an unreadable state was reported as a state")
	}
}
