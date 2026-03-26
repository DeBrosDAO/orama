package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// ClusterTable prints a cluster overview table to w.
func ClusterTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	dur := snap.Duration.Seconds()
	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("Cluster Overview \u2014 %s (%d nodes, collected in %.1fs)",
			snap.Environment, snap.TotalCount(), dur)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 60))
	fmt.Fprintln(w)

	// Header
	fmt.Fprintf(w, "%-18s %-12s %-6s %-6s %-11s %-5s %s\n",
		styleHeader.Render("NODE"),
		styleHeader.Render("ROLE"),
		styleHeader.Render("MEM"),
		styleHeader.Render("DISK"),
		styleHeader.Render("RQLITE"),
		styleHeader.Render("WG"),
		styleHeader.Render("SERVICES"))
	fmt.Fprintln(w, separator(70))

	// Healthy nodes
	for _, cs := range snap.Nodes {
		if cs.Error != nil {
			continue
		}
		r := cs.Report
		if r == nil {
			continue
		}

		host := cs.Node.Host
		role := cs.Node.Role

		// Memory %
		memStr := "--"
		if r.System != nil {
			memStr = fmt.Sprintf("%d%%", r.System.MemUsePct)
		}

		// Disk %
		diskStr := "--"
		if r.System != nil {
			diskStr = fmt.Sprintf("%d%%", r.System.DiskUsePct)
		}

		// RQLite state
		rqliteStr := "--"
		if r.RQLite != nil && r.RQLite.Responsive {
			rqliteStr = r.RQLite.RaftState
		} else if r.RQLite != nil {
			rqliteStr = styleRed.Render("DOWN")
		}

		// WireGuard
		wgStr := statusIcon(r.WireGuard != nil && r.WireGuard.InterfaceUp)

		// Services: active/total
		svcStr := "--"
		if r.Services != nil {
			active := 0
			total := len(r.Services.Services)
			for _, svc := range r.Services.Services {
				if svc.ActiveState == "active" {
					active++
				}
			}
			svcStr = fmt.Sprintf("%d/%d", active, total)
		}

		fmt.Fprintf(w, "%-18s %-12s %-6s %-6s %-11s %-5s %s\n",
			host, role, memStr, diskStr, rqliteStr, wgStr, svcStr)
	}

	// Unreachable nodes
	failed := snap.Failed()
	if len(failed) > 0 {
		fmt.Fprintln(w)
		for _, cs := range failed {
			fmt.Fprintf(w, "%-18s %-12s %s\n",
				styleRed.Render(cs.Node.Host),
				cs.Node.Role,
				styleRed.Render("UNREACHABLE"))
		}
	}

	// Alerts summary
	critCount, warnCount := countAlerts(snap.Alerts)
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Alerts: %s critical, %s warning\n",
		alertCountStr(critCount, monitor.AlertCritical),
		alertCountStr(warnCount, monitor.AlertWarning))

	for _, a := range snap.Alerts {
		if a.Severity == monitor.AlertCritical || a.Severity == monitor.AlertWarning {
			tag := severityTag(a.Severity)
			fmt.Fprintf(w, "  %s %s: %s\n", tag, a.Node, a.Message)
		}
	}

	return nil
}

// ClusterJSON writes the cluster snapshot as JSON.
func ClusterJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type clusterEntry struct {
		Host     string `json:"host"`
		Role     string `json:"role"`
		MemPct   int    `json:"mem_pct"`
		DiskPct  int    `json:"disk_pct"`
		RQLite   string `json:"rqlite_state"`
		WGUp     bool   `json:"wg_up"`
		Services string `json:"services"`
		Status   string `json:"status"`
		Error    string `json:"error,omitempty"`
	}

	var entries []clusterEntry
	for _, cs := range snap.Nodes {
		e := clusterEntry{
			Host: cs.Node.Host,
			Role: cs.Node.Role,
		}
		if cs.Error != nil {
			e.Status = "unreachable"
			e.Error = cs.Error.Error()
			entries = append(entries, e)
			continue
		}
		r := cs.Report
		if r == nil {
			e.Status = "unreachable"
			entries = append(entries, e)
			continue
		}
		e.Status = "ok"
		if r.System != nil {
			e.MemPct = r.System.MemUsePct
			e.DiskPct = r.System.DiskUsePct
		}
		if r.RQLite != nil && r.RQLite.Responsive {
			e.RQLite = r.RQLite.RaftState
		}
		e.WGUp = r.WireGuard != nil && r.WireGuard.InterfaceUp
		if r.Services != nil {
			active := 0
			total := len(r.Services.Services)
			for _, svc := range r.Services.Services {
				if svc.ActiveState == "active" {
					active++
				}
			}
			e.Services = fmt.Sprintf("%d/%d", active, total)
		}
		entries = append(entries, e)
	}

	return writeJSON(w, entries)
}

// countAlerts returns the number of critical and warning alerts.
func countAlerts(alerts []monitor.Alert) (crit, warn int) {
	for _, a := range alerts {
		switch a.Severity {
		case monitor.AlertCritical:
			crit++
		case monitor.AlertWarning:
			warn++
		}
	}
	return
}

// severityTag returns a colored tag like [CRIT], [WARN], [INFO].
func severityTag(s monitor.AlertSeverity) string {
	switch s {
	case monitor.AlertCritical:
		return styleRed.Render("[CRIT]")
	case monitor.AlertWarning:
		return styleYellow.Render("[WARN]")
	case monitor.AlertInfo:
		return styleMuted.Render("[INFO]")
	default:
		return styleMuted.Render("[????]")
	}
}

// alertCountStr renders the count with appropriate color.
func alertCountStr(count int, sev monitor.AlertSeverity) string {
	s := fmt.Sprintf("%d", count)
	if count > 0 {
		return severityColor(sev).Render(s)
	}
	return s
}
