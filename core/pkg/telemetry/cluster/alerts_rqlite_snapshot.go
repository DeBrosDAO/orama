package cluster

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// checkSnapshotTermAhead flags a node whose latest raft snapshot carries a
// term above the node's current term. Raft never produces that: a snapshot is
// taken at an applied entry, whose term is at most the current one. It is
// left by a recovery that restarted the cluster's term below its recovery
// snapshot (orama node recover-raft before 2026-10-03 deleted the leader's
// raft.db). rqlite orders snapshots by term first, so that snapshot stays
// "newest": every later snapshot is reaped as older, a node that needs a
// snapshot is sent the stale one and never catches up, and a restarted node
// restores it and stalls the same way (docs/COMMON_PROBLEMS.md).
func checkSnapshotTermAhead(reports []*report.NodeReport) []Alert {
	var alerts []Alert
	for _, r := range reports {
		if r.RQLite == nil || !r.RQLite.Responsive || r.RQLite.Term == 0 {
			continue
		}
		if r.RQLite.LastSnapshotTerm > r.RQLite.Term {
			alerts = append(alerts, Alert{AlertCritical, "rqlite", nodeHost(r),
				fmt.Sprintf("Raft snapshot term %d is above the current term %d: a lagging or restarted node cannot catch up; do not restart rqlite (see COMMON_PROBLEMS.md)",
					r.RQLite.LastSnapshotTerm, r.RQLite.Term)})
		}
	}
	return alerts
}
