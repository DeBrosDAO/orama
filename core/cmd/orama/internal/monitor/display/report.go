package display

import (
	"io"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// NoLeader is the summary's rqlite_leader when no node is the leader.
const NoLeader = "none"

// Node statuses in the report and the node JSON.
const (
	NodeStatusOK          = "ok"
	NodeStatusDegraded    = "degraded"
	NodeStatusUnreachable = "unreachable"
	nodeStatusUnknown     = "unknown"
)

// Summary values: rqlite_quorum, wg_mesh_status and service_health each use
// some of these.
const (
	SummaryOK       = "ok"
	SummaryDegraded = "degraded"
	SummaryLost     = "lost"
	SummaryDown     = "down"
	SummaryCritical = "critical"
	SummaryUnknown  = "unknown"
)

// clusterAlertNode is the Node of an alert about the whole cluster.
const clusterAlertNode = "cluster"

// unitFailed is systemd's ActiveState for a failed unit.
const unitFailed = "failed"

// fullReport is the JSON `orama status report` writes. e2e/lifecycle decodes
// it as its only view of a cluster, so it is a contract: fields may be added,
// never renamed or removed (report_contract_test.go holds it).
type fullReport struct {
	Meta struct {
		Environment  string    `json:"environment"`
		CollectedAt  time.Time `json:"collected_at"`
		DurationSec  float64   `json:"duration_seconds"`
		NodeCount    int       `json:"node_count"`
		HealthyCount int       `json:"healthy_count"`
		FailedCount  int       `json:"failed_count"`
	} `json:"meta"`
	Summary struct {
		RQLiteLeader   string          `json:"rqlite_leader"`
		RQLiteQuorum   string          `json:"rqlite_quorum"`
		WGMeshStatus   string          `json:"wg_mesh_status"`
		ServiceHealth  string          `json:"service_health"`
		CriticalAlerts int             `json:"critical_alerts"`
		WarningAlerts  int             `json:"warning_alerts"`
		Verdict        cluster.Verdict `json:"verdict"`
	} `json:"summary"`
	Components []cluster.Component `json:"components"`
	Alerts     []cluster.Alert     `json:"alerts"`
	Nodes      []nodeEntry         `json:"nodes"`
}

type nodeEntry struct {
	Host   string             `json:"host"`
	Role   string             `json:"role"`
	Status string             `json:"status"` // NodeStatusOK, NodeStatusDegraded or NodeStatusUnreachable
	Report *report.NodeReport `json:"report,omitempty"`
	Error  string             `json:"error,omitempty"`
	// ReportAgeSec is how old the node's report was when the snapshot was
	// assembled: the gateway gathers each node's telemetry on a timer. Zero
	// over --ssh, which collects it on the spot.
	ReportAgeSec int `json:"report_age_sec"`
}

// FullReport writes the whole snapshot as one JSON document, with a summary
// and the verdict on top, for scripts, the lifecycle harness and LLMs.
func FullReport(snap *cluster.ClusterSnapshot, w io.Writer) error {
	return writeJSON(w, buildFullReport(snap))
}

func buildFullReport(snap *cluster.ClusterSnapshot) fullReport {
	fr := fullReport{Alerts: snap.Alerts}
	fr.Meta.Environment = snap.Environment
	fr.Meta.CollectedAt = snap.CollectedAt
	fr.Meta.DurationSec = (time.Duration(snap.DurationMS) * time.Millisecond).Seconds()
	fr.Meta.NodeCount = snap.TotalCount()
	fr.Meta.HealthyCount = snap.HealthyCount()
	fr.Meta.FailedCount = len(snap.Failed())

	fr.Components = cluster.Components(snap)
	fr.Summary.Verdict = cluster.Summarize(snap, fr.Components)
	fr.Summary.RQLiteLeader = findRQLiteLeader(snap)
	fr.Summary.RQLiteQuorum = computeQuorumStatus(snap)
	fr.Summary.WGMeshStatus = computeWGMeshStatus(snap)
	fr.Summary.ServiceHealth = computeServiceHealth(snap)
	fr.Summary.CriticalAlerts = fr.Summary.Verdict.Critical
	fr.Summary.WarningAlerts = fr.Summary.Verdict.Warning

	fr.Nodes = reportNodes(snap)
	return fr
}

// reportNodes is a report entry per node, degraded when a critical alert
// names it.
func reportNodes(snap *cluster.ClusterSnapshot) []nodeEntry {
	criticalHosts := map[string]bool{}
	for _, a := range snap.Alerts {
		if a.Severity == cluster.AlertCritical && a.Node != "" && a.Node != clusterAlertNode {
			criticalHosts[a.Node] = true
		}
	}
	out := []nodeEntry{}
	for _, cs := range snap.Nodes {
		ne := nodeEntry{Host: cs.Node.Host, Role: cs.Node.Role, Status: NodeStatusUnreachable, Error: cs.Err, ReportAgeSec: cs.ReportAgeSec}
		if cs.Err == "" && cs.Report != nil {
			ne.Status, ne.Report = NodeStatusOK, cs.Report
			if criticalHosts[cs.Node.Host] {
				ne.Status = NodeStatusDegraded
			}
		}
		out = append(out, ne)
	}
	return out
}

// findRQLiteLeader returns the host of the RQLite leader, or NoLeader.
func findRQLiteLeader(snap *cluster.ClusterSnapshot) string {
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.RQLite != nil && cs.Report.RQLite.RaftState == view.RaftLeader {
			return cs.Node.Host
		}
	}
	return NoLeader
}

// computeQuorumStatus returns "ok", "degraded", "lost" or "unknown".
func computeQuorumStatus(snap *cluster.ClusterSnapshot) string {
	total, responsive := 0, 0
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.RQLite != nil {
			total++
			if cs.Report.RQLite.Responsive {
				responsive++
			}
		}
	}
	switch {
	case total == 0:
		return SummaryUnknown
	case responsive >= total/2+1:
		return SummaryOK
	case responsive > 0:
		return SummaryDegraded
	default:
		return SummaryLost
	}
}

// computeWGMeshStatus returns "ok", "degraded", "down" or "unknown".
func computeWGMeshStatus(snap *cluster.ClusterSnapshot) string {
	total, up := 0, 0
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.WireGuard != nil {
			total++
			if cs.Report.WireGuard.InterfaceUp {
				up++
			}
		}
	}
	switch {
	case total == 0:
		return SummaryUnknown
	case up == total:
		return SummaryOK
	case up > 0:
		return SummaryDegraded
	default:
		return SummaryDown
	}
}

// computeServiceHealth returns "ok", "degraded", "critical" or "unknown".
func computeServiceHealth(snap *cluster.ClusterSnapshot) string {
	total, failed := 0, 0
	for _, cs := range snap.Nodes {
		if cs.Report == nil || cs.Report.Services == nil {
			continue
		}
		for _, svc := range cs.Report.Services.Services {
			total++
			if svc.ActiveState == unitFailed {
				failed++
			}
		}
	}
	switch {
	case total == 0:
		return SummaryUnknown
	case failed == 0:
		return SummaryOK
	case failed < total/2:
		return SummaryDegraded
	default:
		return SummaryCritical
	}
}
