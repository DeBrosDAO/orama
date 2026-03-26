package display

import (
	"io"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
)

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
		RQLiteLeader   string `json:"rqlite_leader"`
		RQLiteQuorum   string `json:"rqlite_quorum"`
		WGMeshStatus   string `json:"wg_mesh_status"`
		ServiceHealth  string `json:"service_health"`
		CriticalAlerts int    `json:"critical_alerts"`
		WarningAlerts  int    `json:"warning_alerts"`
	} `json:"summary"`
	Alerts []monitor.Alert `json:"alerts"`
	Nodes  []nodeEntry     `json:"nodes"`
}

type nodeEntry struct {
	Host   string             `json:"host"`
	Role   string             `json:"role"`
	Status string             `json:"status"` // "ok", "unreachable", "degraded"
	Report *report.NodeReport `json:"report,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// FullReport outputs the LLM-optimized JSON report to w.
func FullReport(snap *monitor.ClusterSnapshot, w io.Writer) error {
	fr := fullReport{}

	// Meta
	fr.Meta.Environment = snap.Environment
	fr.Meta.CollectedAt = snap.CollectedAt
	fr.Meta.DurationSec = snap.Duration.Seconds()
	fr.Meta.NodeCount = snap.TotalCount()
	fr.Meta.HealthyCount = snap.HealthyCount()
	fr.Meta.FailedCount = len(snap.Failed())

	// Summary
	fr.Summary.RQLiteLeader = findRQLiteLeader(snap)
	fr.Summary.RQLiteQuorum = computeQuorumStatus(snap)
	fr.Summary.WGMeshStatus = computeWGMeshStatus(snap)
	fr.Summary.ServiceHealth = computeServiceHealth(snap)

	crit, warn := countAlerts(snap.Alerts)
	fr.Summary.CriticalAlerts = crit
	fr.Summary.WarningAlerts = warn

	// Alerts
	fr.Alerts = snap.Alerts

	// Build set of hosts with critical alerts for "degraded" detection
	criticalHosts := map[string]bool{}
	for _, a := range snap.Alerts {
		if a.Severity == monitor.AlertCritical && a.Node != "" && a.Node != "cluster" {
			criticalHosts[a.Node] = true
		}
	}

	// Nodes
	for _, cs := range snap.Nodes {
		ne := nodeEntry{
			Host: cs.Node.Host,
			Role: cs.Node.Role,
		}
		if cs.Error != nil {
			ne.Status = "unreachable"
			ne.Error = cs.Error.Error()
		} else if cs.Report != nil {
			if criticalHosts[cs.Node.Host] {
				ne.Status = "degraded"
			} else {
				ne.Status = "ok"
			}
			ne.Report = cs.Report
		} else {
			ne.Status = "unreachable"
		}
		fr.Nodes = append(fr.Nodes, ne)
	}

	return writeJSON(w, fr)
}

// findRQLiteLeader returns the host of the RQLite leader, or "none".
func findRQLiteLeader(snap *monitor.ClusterSnapshot) string {
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.RQLite != nil && cs.Report.RQLite.RaftState == "Leader" {
			return cs.Node.Host
		}
	}
	return "none"
}

// computeQuorumStatus returns "ok", "degraded", or "lost".
func computeQuorumStatus(snap *monitor.ClusterSnapshot) string {
	total := 0
	responsive := 0
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.RQLite != nil {
			total++
			if cs.Report.RQLite.Responsive {
				responsive++
			}
		}
	}
	if total == 0 {
		return "unknown"
	}
	quorum := (total / 2) + 1
	if responsive >= quorum {
		return "ok"
	}
	if responsive > 0 {
		return "degraded"
	}
	return "lost"
}

// computeWGMeshStatus returns "ok", "degraded", or "down".
func computeWGMeshStatus(snap *monitor.ClusterSnapshot) string {
	totalWG := 0
	upCount := 0
	for _, cs := range snap.Nodes {
		if cs.Report != nil && cs.Report.WireGuard != nil {
			totalWG++
			if cs.Report.WireGuard.InterfaceUp {
				upCount++
			}
		}
	}
	if totalWG == 0 {
		return "unknown"
	}
	if upCount == totalWG {
		return "ok"
	}
	if upCount > 0 {
		return "degraded"
	}
	return "down"
}

// computeServiceHealth returns "ok", "degraded", or "critical".
func computeServiceHealth(snap *monitor.ClusterSnapshot) string {
	totalSvc := 0
	failedSvc := 0
	for _, cs := range snap.Nodes {
		if cs.Report == nil || cs.Report.Services == nil {
			continue
		}
		for _, svc := range cs.Report.Services.Services {
			totalSvc++
			if svc.ActiveState == "failed" {
				failedSvc++
			}
		}
	}
	if totalSvc == 0 {
		return "unknown"
	}
	if failedSvc == 0 {
		return "ok"
	}
	if failedSvc < totalSvc/2 {
		return "degraded"
	}
	return "critical"
}
