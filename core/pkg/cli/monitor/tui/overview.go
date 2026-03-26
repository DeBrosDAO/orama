package tui

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderOverview renders the Overview tab: cluster summary, node table, alert summary.
func renderOverview(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	var b strings.Builder

	// -- Cluster Summary --
	b.WriteString(styleBold.Render("Cluster Summary"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n")

	healthy := snap.HealthyCount()
	total := snap.TotalCount()
	failed := total - healthy

	healthColor := styleHealthy
	if failed > 0 {
		healthColor = styleWarning
	}
	if healthy == 0 && total > 0 {
		healthColor = styleCritical
	}

	b.WriteString(fmt.Sprintf("  Environment:  %s\n", styleBold.Render(snap.Environment)))
	b.WriteString(fmt.Sprintf("  Nodes:        %s / %d\n", healthColor.Render(fmt.Sprintf("%d healthy", healthy)), total))
	if failed > 0 {
		b.WriteString(fmt.Sprintf("  Failed:       %s\n", styleCritical.Render(fmt.Sprintf("%d", failed))))
	}
	b.WriteString(fmt.Sprintf("  Collect time: %s\n", styleMuted.Render(snap.Duration.Truncate(1e6).String())))
	b.WriteString("\n")

	// -- Node Table --
	b.WriteString(styleBold.Render("Nodes"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n")

	// Header row
	b.WriteString(fmt.Sprintf("  %-18s %-8s %-10s %-8s %-8s %-8s %-10s\n",
		headerStyle.Render("HOST"),
		headerStyle.Render("STATUS"),
		headerStyle.Render("ROLE"),
		headerStyle.Render("CPU"),
		headerStyle.Render("MEM%"),
		headerStyle.Render("DISK%"),
		headerStyle.Render("RQLITE"),
	))

	for _, cs := range snap.Nodes {
		if cs.Error != nil {
			b.WriteString(fmt.Sprintf("  %-18s %s  %s\n",
				cs.Node.Host,
				styleCritical.Render("FAIL"),
				styleMuted.Render(truncateStr(cs.Error.Error(), 40)),
			))
			continue
		}
		r := cs.Report
		if r == nil {
			continue
		}

		host := r.PublicIP
		if host == "" {
			host = r.Hostname
		}

		var status string
		if cs.Error == nil && r != nil {
			status = styleHealthy.Render("OK")
		} else {
			status = styleCritical.Render("FAIL")
		}

		role := cs.Node.Role
		if role == "" {
			role = "node"
		}

		cpuStr := "-"
		memStr := "-"
		diskStr := "-"
		if r.System != nil {
			cpuStr = fmt.Sprintf("%.1f", r.System.LoadAvg1)
			memStr = colorPct(r.System.MemUsePct)
			diskStr = colorPct(r.System.DiskUsePct)
		}

		rqliteStr := "-"
		if r.RQLite != nil {
			if r.RQLite.Responsive {
				rqliteStr = styleHealthy.Render(r.RQLite.RaftState)
			} else {
				rqliteStr = styleCritical.Render("DOWN")
			}
		}

		b.WriteString(fmt.Sprintf("  %-18s %-8s %-10s %-8s %-8s %-8s %-10s\n",
			host, status, role, cpuStr, memStr, diskStr, rqliteStr))
	}
	b.WriteString("\n")

	// -- Alert Summary --
	critCount, warnCount, infoCount := countAlertsBySeverity(snap.Alerts)
	b.WriteString(styleBold.Render("Alerts"))
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		styleCritical.Render(fmt.Sprintf("%d critical", critCount)),
		styleWarning.Render(fmt.Sprintf("%d warning", warnCount)),
		styleMuted.Render(fmt.Sprintf("%d info", infoCount)),
	))

	if critCount > 0 {
		b.WriteString("\n")
		for _, a := range snap.Alerts {
			if a.Severity == monitor.AlertCritical {
				b.WriteString(fmt.Sprintf("  %s [%s] %s: %s\n",
					styleCritical.Render("CRIT"),
					a.Subsystem,
					a.Node,
					a.Message,
				))
			}
		}
	}

	return b.String()
}

// colorPct returns a percentage string colored by threshold.
func colorPct(pct int) string {
	s := fmt.Sprintf("%d%%", pct)
	switch {
	case pct >= 90:
		return styleCritical.Render(s)
	case pct >= 75:
		return styleWarning.Render(s)
	default:
		return styleHealthy.Render(s)
	}
}

// countAlertsBySeverity counts alerts by severity level.
func countAlertsBySeverity(alerts []monitor.Alert) (crit, warn, info int) {
	for _, a := range alerts {
		switch a.Severity {
		case monitor.AlertCritical:
			crit++
		case monitor.AlertWarning:
			warn++
		case monitor.AlertInfo:
			info++
		}
	}
	return
}

// truncateStr truncates a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// separator returns a dashed line of the given width.
func separator(width int) string {
	if width <= 0 {
		width = 80
	}
	return styleMuted.Render(strings.Repeat("\u2500", width))
}
