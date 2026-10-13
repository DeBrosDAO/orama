package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func rqliteNode(host string, term, snapTerm uint64, responsive bool) *report.NodeReport {
	return &report.NodeReport{PublicIP: host, RQLite: &report.RQLiteReport{Responsive: responsive, Term: term, LastSnapshotTerm: snapTerm}}
}

func TestCheckSnapshotTermAhead_poisonedNodeIsCritical(t *testing.T) {
	alerts := checkSnapshotTermAhead([]*report.NodeReport{rqliteNode("10.0.0.1", 1, 1, true), rqliteNode("10.0.0.5", 1, 21, true)})
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert for the node holding a term-21 snapshot at term 1, got %v", alerts)
	}
	if alerts[0].Severity != AlertCritical || !strings.Contains(alerts[0].Message, "snapshot term 21 is above the current term 1") {
		t.Errorf("alert = %+v, want critical naming both terms", alerts[0])
	}
}

func TestCheckSnapshotTermAhead_healthyClusterIsQuiet(t *testing.T) {
	alerts := checkSnapshotTermAhead([]*report.NodeReport{rqliteNode("10.0.0.1", 21, 21, true), rqliteNode("10.0.0.2", 22, 21, true)})
	if len(alerts) != 0 {
		t.Fatalf("a snapshot at or below the current term is normal, got %v", alerts)
	}
}

func TestCheckSnapshotTermAhead_unknownTermIsSkipped(t *testing.T) {
	alerts := checkSnapshotTermAhead([]*report.NodeReport{
		{PublicIP: "10.0.0.3"},
		rqliteNode("10.0.0.4", 0, 21, true),
		rqliteNode("10.0.0.5", 1, 21, false),
	})
	if len(alerts) != 0 {
		t.Fatalf("no rqlite, an unknown term (an old report) or an unresponsive node must not alert, got %v", alerts)
	}
}

func TestCheckRaftTermConsistency_reportWithoutTermIsLeftOut(t *testing.T) {
	alerts := checkRaftTermConsistency([]*report.NodeReport{rqliteNode("10.0.0.1", 21, 21, true), rqliteNode("10.0.0.2", 0, 0, true)})
	if len(alerts) != 0 {
		t.Fatalf("a not-yet-upgraded node reporting no term must not read as divergence, got %v", alerts)
	}
}

func TestCheckRaftTermConsistency_divergenceWarns(t *testing.T) {
	alerts := checkRaftTermConsistency([]*report.NodeReport{rqliteNode("10.0.0.1", 21, 21, true), rqliteNode("10.0.0.2", 25, 21, true)})
	if len(alerts) != 1 || alerts[0].Severity != AlertWarning {
		t.Fatalf("terms 21 and 25: want one warning, got %v", alerts)
	}
}
