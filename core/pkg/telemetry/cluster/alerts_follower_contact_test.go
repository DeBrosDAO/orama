package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func raftNode(host, state, lastContact string, applied uint64) *report.NodeReport {
	return &report.NodeReport{PublicIP: host, RQLite: &report.RQLiteReport{
		Responsive: true, RaftState: state, LastContact: lastContact, Applied: applied, Commit: applied,
	}}
}

// The nodes of one snapshot are read at slightly different moments, so under
// steady writes their applied indexes differ by the writes made in between: a
// healthy stagenet read as 101 behind. That spread is not lag.
func TestCheckFollowerContact_appliedSpreadOfOneSnapshotIsNotLag(t *testing.T) {
	alerts := checkFollowerContact([]*report.NodeReport{
		raftNode("10.0.0.1", report.RaftLeader, "0", 5101),
		raftNode("10.0.0.2", report.RaftFollower, "31.2ms", 5000),
		raftNode("10.0.0.3", report.RaftFollower, "12ms", 5050),
	})
	if len(alerts) != 0 {
		t.Fatalf("followers in contact with the leader must not alert on the applied spread, got %v", alerts)
	}
}

func TestCheckFollowerContact_staleFollowerWarns(t *testing.T) {
	alerts := checkFollowerContact([]*report.NodeReport{
		raftNode("10.0.0.1", report.RaftLeader, "0", 100),
		raftNode("10.0.0.2", report.RaftFollower, "7.5s", 100),
	})
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert for a follower silent for 7.5s, got %v", alerts)
	}
	a := alerts[0]
	if a.Severity != AlertWarning || a.Node != "10.0.0.2" || !strings.Contains(a.Message, "last contact 7.5s") {
		t.Errorf("alert = %+v, want a warning on 10.0.0.2 naming the 7.5s", a)
	}
}

func TestCheckFollowerContact_neverContactedWarns(t *testing.T) {
	alerts := checkFollowerContact([]*report.NodeReport{raftNode("10.0.0.2", report.RaftFollower, "never", 0)})
	if len(alerts) != 1 {
		t.Fatalf("a follower that never heard from a leader must alert, got %v", alerts)
	}
}

func TestCheckFollowerContact_leaderUnknownAndUnresponsiveAreSkipped(t *testing.T) {
	unresponsive := raftNode("10.0.0.4", report.RaftFollower, "30s", 1)
	unresponsive.RQLite.Responsive = false
	alerts := checkFollowerContact([]*report.NodeReport{
		raftNode("10.0.0.1", report.RaftLeader, "30s", 1),
		raftNode("10.0.0.2", report.RaftFollower, "", 1),
		{PublicIP: "10.0.0.3"},
		unresponsive,
	})
	if len(alerts) != 0 {
		t.Fatalf("a leader, a report without last_contact, no rqlite or an unresponsive node must not alert, got %v", alerts)
	}
}
