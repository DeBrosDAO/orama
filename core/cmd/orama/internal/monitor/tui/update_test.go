package tui

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// The live view shows a node's update finding in the node table, the chain upgrade scheduled for
// it on the Chain tab, and the update as an alert on the Alerts tab.
func TestLiveView_showsTheUpdateAndTheScheduledChainUpgrade(t *testing.T) {
	snap := testSnapshot()
	snap.Nodes[0].Report.Update = &updatenotice.Notice{State: updatenotice.StateAvailable, Mode: "notify", Channel: "stable",
		Current: "0.3.0", Candidate: "0.3.1", Reason: "newer release 0.3.1 (notify)"}
	snap.Nodes[0].Report.Chain = &report.ChainReport{ServiceActive: true, Responsive: true, ChainID: "orama-devnet-1", LatestHeight: 100,
		AvgBlockTimeSec: 6, Upgrade: &report.ChainUpgrade{Name: "v0-4-0", Height: 700}}
	snap.Alerts = cluster.DeriveAlerts(snap)

	m := testModel()
	next, _ := m.Update(updateMsg{u: monitor.Update{State: monitor.LinkLive, Snapshot: snap}, ok: true})
	m = next.(model)

	nodes := press(m, runes("3")).View()
	for _, want := range []string{"UPDATE", "0.3.1 (notify)"} {
		if !strings.Contains(nodes, want) {
			t.Errorf("the Nodes tab misses %q:\n%s", want, nodes)
		}
	}
	chain := press(m, runes("6")).View()
	for _, want := range []string{"Scheduled chain upgrade", "v0-4-0 at height 700", "600 blocks left", "binary NOT staged"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the Chain tab misses %q:\n%s", want, chain)
		}
	}
	// The Alerts tab on its information filter lists the update; the chain upgrade nobody staged
	// is a warning alert in the same list.
	info := press(press(m, runes("0")), runes("i")).View()
	if !strings.Contains(info, "Release 0.3.1 is available on the stable channel") {
		t.Errorf("the Alerts tab misses the update:\n%s", info)
	}
	warned := false
	for _, a := range snap.Alerts {
		warned = warned || (a.Severity == cluster.AlertWarning && a.Subsystem == "chain" &&
			strings.Contains(a.Message, `chain upgrade "v0-4-0" is scheduled at height 700`))
	}
	if !warned {
		t.Errorf("no warning alert for the unstaged chain upgrade in %+v", snap.Alerts)
	}
}
