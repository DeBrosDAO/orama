package fleet

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
)

// Chaos helpers inject failures on purpose. They use systemctl, iptables and
// date directly: they are simulating a crash or a broken clock, not operating
// the node, and there is (by design) no orama command that crashes a service.
// Each registers a cleanup that restores the node and waits until it is.

// Recovery budgets.
const (
	// UnitRecoverBudget is how long a unit has to be active again after a
	// cleanup starts it.
	UnitRecoverBudget = 3 * time.Minute
	// ClockTolerance is how far a node's clock may be from the runner's after
	// a skew is undone.
	ClockTolerance = 5 * time.Second
	recoverPoll    = 2 * time.Second
)

const unitActive = "active"

// Kill sends SIGKILL to every process of unit, the way a crash does. The unit's
// own Restart= policy decides what happens next; the cleanup puts the unit
// back in the state it was in before (see restoreUnit) before the next test.
func (f *Fleet) Kill(t testing.TB, n Node, unit string) {
	t.Helper()
	requireSafe(t, "unit", unit)
	prior := f.Unit(t, n, unit)
	t.Cleanup(func() { f.restoreUnit(t, n, unit, prior) })
	f.MustExec(t, n, "systemctl kill --signal=SIGKILL "+unit)
}

// StopService stops unit cleanly; the cleanup puts it back in the state it
// was in before.
func (f *Fleet) StopService(t testing.TB, n Node, unit string) {
	t.Helper()
	requireSafe(t, "unit", unit)
	prior := f.Unit(t, n, unit)
	t.Cleanup(func() { f.restoreUnit(t, n, unit, prior) })
	f.MustExec(t, n, "systemctl stop "+unit)
}

// restoreUnit clears the failed state the disturbance left (a killed unit
// past its start limit would otherwise refuse to start) and puts unit back
// in its prior state: active again, or stopped when it was not running.
func (f *Fleet) restoreUnit(t testing.TB, n Node, unit, prior string) {
	t.Helper()
	if prior == unitActive {
		f.ensureActive(t, n, unit)
		return
	}
	f.cleanupExec(t, n, "systemctl reset-failed "+unit+" 2>/dev/null; systemctl stop "+unit+"; ! systemctl is-active --quiet "+unit)
}

// ensureActive resets the unit's failed state, starts it if needed and
// polls until it is active.
func (f *Fleet) ensureActive(t testing.TB, n Node, unit string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), UnitRecoverBudget)
	defer cancel()
	sh := f.shellFor(t.Name(), n)
	err := eventually.Poll(ctx, recoverPoll, UnitRecoverBudget, n.Name+" "+unit+" active", func() (bool, error) {
		out, err := sh.Run(ctx, "systemctl is-active "+unit+" || { systemctl reset-failed "+unit+" 2>/dev/null; systemctl start "+unit+"; }")
		if err != nil {
			return false, err
		}
		state := strings.TrimSpace(strings.SplitN(out.Stdout, "\n", 2)[0])
		if state != unitActive {
			return false, fmt.Errorf("%s is %s", unit, state)
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("cleanup: %v — later tests will see a disturbed node", err)
	}
}

// ClockSkew moves node's clock by offset with NTP off. The cleanup sets it back
// to the runner's time, turns NTP on and checks the result.
func (f *Fleet) ClockSkew(t testing.TB, n Node, offset time.Duration) {
	t.Helper()
	t.Cleanup(func() { f.restoreClock(t, n) })
	target := time.Now().Add(offset).Unix()
	f.MustExec(t, n, "timedatectl set-ntp false && date -u -s @"+strconv.FormatInt(target, 10))
}

func (f *Fleet) restoreClock(t testing.TB, n Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), CleanupBudget)
	defer cancel()
	sh := f.shellFor(t.Name(), n)
	cmd := "date -u -s @" + strconv.FormatInt(time.Now().Unix(), 10) + " >/dev/null && timedatectl set-ntp true && date -u +%s"
	out, err := sh.Run(ctx, cmd)
	if err != nil || out.Exit != 0 {
		t.Errorf("cleanup failed to restore the clock on %s: exit %d err %v %s", n.Name, out.Exit, err, out.Stderr)
		return
	}
	remote, err := strconv.ParseInt(strings.TrimSpace(out.Stdout), 10, 64)
	if err != nil {
		t.Errorf("cleanup could not read the clock on %s (%q): %v", n.Name, out.Stdout, err)
		return
	}
	if d := time.Duration(remote-time.Now().Unix()) * time.Second; d > ClockTolerance || d < -ClockTolerance {
		t.Errorf("cleanup left %s's clock %s off", n.Name, d)
	}
}
