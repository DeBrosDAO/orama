package cluster

import "github.com/DeBrosOfficial/network/pkg/hardening"

// hardeningAlerts raises one warning per hardened setting that no longer holds
// on the node (core dumps, swap, ptrace, apport). Warning, not critical: the
// node still serves, so it must not turn the whole cluster degraded, but the
// guarantee that secret-bearing memory stays off the disk is broken until
// someone restores it, so it is never info. Nil (a release that did not
// report it) raises nothing.
func hardeningAlerts(live *hardening.Live, host string) []Alert {
	if live == nil {
		return nil
	}
	var alerts []Alert
	for _, d := range live.Drift() {
		alerts = append(alerts, Alert{AlertWarning, "system", host, "RAM hardening drifted: " + d})
	}
	return alerts
}
