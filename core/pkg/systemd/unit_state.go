package systemd

import (
	"fmt"
	"os/exec"
	"strings"
)

// systemd's LoadState and ActiveState values that teardown decides on.
const (
	loadStateNotFound   = "not-found"
	activeStateInactive = "inactive"
	activeStateFailed   = "failed"
)

// ActiveState is systemd's ActiveState of a unit.
type ActiveState string

// The ActiveState values the platform decides on. Systemd also reports
// "maintenance" and "refreshing", which are neither running nor on their way
// in or out; they are not transitional here.
const (
	ActiveStateActive       ActiveState = "active"
	ActiveStateActivating   ActiveState = "activating"
	ActiveStateDeactivating ActiveState = "deactivating"
	ActiveStateReloading    ActiveState = "reloading"
	ActiveStateInactive     ActiveState = ActiveState(activeStateInactive)
	ActiveStateFailed       ActiveState = ActiveState(activeStateFailed)
)

// Running reports whether the unit is up.
func (a ActiveState) Running() bool { return a == ActiveStateActive }

// Transitional reports whether a start, stop or reload job is in flight. Such a
// unit is neither running nor stopped: a `systemctl start` issued now cancels a
// pending stop job (the stop then fails with "Job canceled"), and one issued
// during a start races it. A caller that would start a unit leaves it alone.
func (a ActiveState) Transitional() bool {
	return a == ActiveStateActivating || a == ActiveStateDeactivating || a == ActiveStateReloading
}

// unitState is what systemd reports about a unit: whether it is loaded, and
// whether it is running.
type unitState struct {
	Load   string
	Active string
	// UnitFile is systemd's UnitFileState ("enabled", "disabled", ...), empty
	// when systemd did not report it.
	UnitFile string
}

// unitFileStatesThatStart are the UnitFileState values of a unit systemd
// starts on its own (at boot, or when its target is reached).
var unitFileStatesThatStart = map[string]bool{"enabled": true, "enabled-runtime": true}

// live reports whether the unit can run: it is running or on its way, or it is
// enabled and so starts on the next boot. A loaded unit that is neither — an
// instance systemd still remembers after it was stopped and disabled — can
// never start again on its own.
func (s unitState) live() bool {
	if s.Active != activeStateInactive && s.Active != activeStateFailed {
		return true
	}
	return unitFileStatesThatStart[s.UnitFile]
}

// loaded reports whether systemd has a unit file for it. A unit that is not
// found has nothing to stop and nothing to disable.
func (s unitState) loaded() bool { return s.Load != loadStateNotFound }

// readUnitState asks systemd for a unit's state through the unitState seam.
func (m *Manager) readUnitState(unit string) (unitState, error) {
	if m.unitState != nil {
		return m.unitState(unit)
	}
	return queryUnitState(unit)
}

// queryUnitState reads LoadState and ActiveState with `systemctl show`, a query
// that needs no privilege. `show` exits 0 for a unit that does not exist and
// reports it as not-found, so a non-zero exit is a failure to ask.
func queryUnitState(unit string) (unitState, error) {
	out, err := exec.Command("systemctl", "show", "-p", "LoadState", "-p", "ActiveState", "-p", "UnitFileState", unit).CombinedOutput()
	if err != nil {
		return unitState{}, fmt.Errorf("systemctl show %s: %w; output: %s", unit, err, strings.TrimSpace(string(out)))
	}
	return parseUnitState(string(out))
}

// parseUnitState reads the LoadState= and ActiveState= lines of `systemctl
// show`. Both must be present: a reply without them is not an answer.
func parseUnitState(out string) (unitState, error) {
	var s unitState
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "LoadState="); ok {
			s.Load = v
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ActiveState="); ok {
			s.Active = v
		}
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "UnitFileState="); ok {
			s.UnitFile = v
		}
	}
	if s.Load == "" || s.Active == "" {
		return unitState{}, fmt.Errorf("systemctl show reported no LoadState/ActiveState: %q", strings.TrimSpace(out))
	}
	return s, nil
}
