package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func healthyIPFS() *report.IPFSReport {
	return &report.IPFSReport{
		DaemonActive: true, ClusterActive: true, SwarmPeerCount: 2,
		HasSwarmKey: true, BootstrapEmpty: true,
	}
}

func pinLockAlerts(alerts []Alert) []Alert {
	var out []Alert
	for _, a := range alerts {
		if strings.Contains(a.Message, "pin lock") {
			out = append(out, a)
		}
	}
	return out
}

// Bug 2722: a pin/add of content no peer had ran for hours, and every repo GC
// run timed out behind it. The report saw it; nothing alerted.
func TestCheckNodeIPFS_stalledPinLockAlerts(t *testing.T) {
	ipfs := healthyIPFS()
	ipfs.OldestPinLockCmd = "pin/add"
	ipfs.OldestPinLockAgeSeconds = 5*3600 + 47*60
	alerts := pinLockAlerts(checkNodeIPFS(&report.NodeReport{IPFS: ipfs}, "10.0.0.3"))
	if len(alerts) != 1 {
		t.Fatalf("want one pin-lock alert, got %v", alerts)
	}
	a := alerts[0]
	if a.Severity != AlertWarning || a.Subsystem != "ipfs" || a.Node != "10.0.0.3" {
		t.Errorf("unexpected alert %+v", a)
	}
	if !strings.Contains(a.Message, "pin/add") || !strings.Contains(a.Message, "5h47m") {
		t.Errorf("alert %q does not name the command and its age", a.Message)
	}
}

// A pin that has run for less than one GC timeout is ordinary work.
func TestCheckNodeIPFS_pinLockBelowThresholdIsQuiet(t *testing.T) {
	for _, secs := range []int64{0, 60, int64(ipfsPinLockStallAge.Seconds())} {
		ipfs := healthyIPFS()
		ipfs.OldestPinLockCmd = "pin/add"
		ipfs.OldestPinLockAgeSeconds = secs
		if alerts := pinLockAlerts(checkNodeIPFS(&report.NodeReport{IPFS: ipfs}, "10.0.0.3")); len(alerts) != 0 {
			t.Errorf("age %ds alerted: %v", secs, alerts)
		}
	}
}

func TestCheckNodeIPFS_healthyAndNil(t *testing.T) {
	if alerts := checkNodeIPFS(&report.NodeReport{}, "10.0.0.3"); len(alerts) != 0 {
		t.Errorf("no IPFS report alerted: %v", alerts)
	}
	if alerts := checkNodeIPFS(&report.NodeReport{IPFS: healthyIPFS()}, "10.0.0.3"); len(alerts) != 0 {
		t.Errorf("healthy IPFS alerted: %v", alerts)
	}
}
