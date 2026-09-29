//go:build e2e_fleet

package monitoringchaos

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

const (
	// alertBudget: a report is collected every 10s and the snapshot cached
	// 5s; a minute covers a hung collection too.
	alertBudget = 2 * time.Minute
	// Clock offsets on either side of the 5s warning and 60s critical
	// thresholds (core/pkg/telemetry/cluster/alerts_cluster.go).
	skewWarn     = 20 * time.Second
	skewCritical = 90 * time.Second
)

// alertMatch reports whether r carries an alert of severity whose subsystem
// and message contain the given fragments.
func alertMatch(r *monitor.Report, severity, subsystem, fragment string) bool {
	for _, a := range r.Alerts {
		if a.Severity == severity && a.Subsystem == subsystem && strings.Contains(a.Message, fragment) {
			return true
		}
	}
	return false
}

// awaitAlert polls the operator's report until the alert appears.
func awaitAlert(t *testing.T, severity, subsystem, fragment string) {
	t.Helper()
	f := harness.Fleet(t)
	eventually.Require(t, edge.PollEvery, alertBudget, fmt.Sprintf("a %s %s alert %q", severity, subsystem, fragment), func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		if alertMatch(r, severity, subsystem, fragment) {
			return true, nil
		}
		return false, fmt.Errorf("alerts: %+v", r.Alerts)
	})
}

// awaitCleared waits, from a cleanup, until no alert matching fragment is
// left: the injection was undone and the monitor saw it.
func awaitCleared(t *testing.T, subsystem, fragment string) {
	f := harness.Fleet(t)
	ctx, cancel := context.WithTimeout(context.Background(), alertBudget+time.Minute)
	defer cancel()
	err := eventually.Poll(ctx, edge.PollEvery, alertBudget+time.Minute, "the alert to clear", func() (bool, error) {
		r, err := monitor.Get(ctx, harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		for _, sev := range []string{"critical", "warning", "info"} {
			if alertMatch(r, sev, subsystem, fragment) {
				return false, fmt.Errorf("still alerting: %q", fragment)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("cleanup: %v", err)
	}
}

// target is a follower, so no test here moves raft leadership.
func target(t *testing.T) fleet.Node {
	t.Helper()
	return infra.Followers(t, infra.RequireHealthy(t))[0]
}

// TestAlerts_clockSkewWarningThenCritical: a node 20s off raises the
// cluster-wide clock-skew warning; 90s off, the monitor raises a critical
// alert about it (the skew itself, or the node's collection failing because
// its stamps leave the ±60s window) (docs/MONITORING.md "Clock Skew": warning
// beyond 5s, critical beyond 60s). ClockSkew restores NTP at cleanup.
func TestAlerts_clockSkewWarningThenCritical(t *testing.T) {
	f := harness.Fleet(t)
	n := target(t)
	t.Cleanup(func() { awaitCleared(t, "system", "Clock skew") })
	f.ClockSkew(t, n, skewWarn)
	awaitAlert(t, "warning", "system", "Clock skew")
	f.ClockSkew(t, n, skewCritical)
	eventually.Require(t, edge.PollEvery, alertBudget, "a critical alert about "+n.Name, func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		for _, a := range r.Alerts {
			if a.Severity == "critical" && (strings.Contains(a.Message, "Clock skew") || a.Node == n.PublicIP) {
				return true, nil
			}
		}
		return false, fmt.Errorf("alerts: %+v", r.Alerts)
	})
}

// TestAlerts_failedUnitWarned: a failed systemd unit on a node is reported
// by name (docs/MONITORING.md "Services: Systemd state"; `systemctl
// --failed`). The unit is a transient that exits 1, reset at cleanup.
func TestAlerts_failedUnitWarned(t *testing.T) {
	f := harness.Fleet(t)
	n := target(t)
	unit := edge.RandomLabel(t, "e2e-fail-") + ".service"
	t.Cleanup(func() { awaitCleared(t, "service", unit) })
	t.Cleanup(func() {
		edge.RunInCleanup(t, f, n, "systemctl reset-failed "+unit+" 2>/dev/null; ! systemctl --failed --no-legend | grep -q "+unit)
	})
	f.MustExec(t, n, "systemd-run --unit="+unit+" --no-block /bin/false")
	awaitAlert(t, "warning", "service", "Failed systemd unit: "+unit)
}

// TestAlerts_coreServiceStoppedWarned: a core service stopped on a node is
// a warning naming it (docs/MONITORING.md "Services").
func TestAlerts_coreServiceStoppedWarned(t *testing.T) {
	f := harness.Fleet(t)
	n := target(t)
	t.Cleanup(func() { awaitCleared(t, "service", "orama-namespace-tor@index is") })
	f.StopService(t, n, edge.TorUnit)
	awaitAlert(t, "warning", "service", "orama-namespace-tor@index is inactive")
}

// TestAlerts_firewallInactiveCritical: a node whose UFW is disabled raises a
// critical alert (docs/MONITORING.md "Alert Severities": UFW inactive). The
// firewall is enabled again at cleanup, before anything else, and checked.
func TestAlerts_firewallInactiveCritical(t *testing.T) {
	f := harness.Fleet(t)
	n := target(t)
	t.Cleanup(func() { awaitCleared(t, "network", "UFW firewall is inactive") })
	t.Cleanup(func() {
		edge.RunInCleanup(t, f, n, "ufw --force enable >/dev/null && ufw status | grep -q '^Status: active'")
	})
	f.MustExec(t, n, "ufw --force disable")
	awaitAlert(t, "critical", "network", "UFW firewall is inactive")
}
