package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderAlertsTab renders all alerts sorted by severity.
func renderAlertsTab(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	if len(snap.Alerts) == 0 {
		return styleHealthy.Render("  No alerts. All systems nominal.")
	}

	var b strings.Builder

	critCount, warnCount, infoCount := countAlertsBySeverity(snap.Alerts)
	b.WriteString(styleBold.Render("Alerts"))
	b.WriteString(fmt.Sprintf("  %s  %s  %s\n",
		styleCritical.Render(fmt.Sprintf("%d critical", critCount)),
		styleWarning.Render(fmt.Sprintf("%d warning", warnCount)),
		styleMuted.Render(fmt.Sprintf("%d info", infoCount)),
	))
	b.WriteString(separator(width))
	b.WriteString("\n\n")

	// Sort: critical first, then warning, then info
	sorted := make([]monitor.Alert, len(snap.Alerts))
	copy(sorted, snap.Alerts)
	sort.Slice(sorted, func(i, j int) bool {
		return severityRank(sorted[i].Severity) < severityRank(sorted[j].Severity)
	})

	// Group by severity
	currentSev := monitor.AlertSeverity("")
	for _, a := range sorted {
		if a.Severity != currentSev {
			currentSev = a.Severity
			label := strings.ToUpper(string(a.Severity))
			b.WriteString(severityStyle(string(a.Severity)).Render(fmt.Sprintf("  ── %s ", label)))
			b.WriteString("\n")
		}

		sevTag := formatSeverityTag(a.Severity)
		b.WriteString(fmt.Sprintf("  %s  %-12s %-18s %s\n",
			sevTag,
			styleMuted.Render("["+a.Subsystem+"]"),
			a.Node,
			a.Message,
		))
	}

	return b.String()
}

// severityRank returns a sort rank (lower = more severe).
func severityRank(s monitor.AlertSeverity) int {
	switch s {
	case monitor.AlertCritical:
		return 0
	case monitor.AlertWarning:
		return 1
	case monitor.AlertInfo:
		return 2
	default:
		return 3
	}
}

// formatSeverityTag returns a styled severity label.
func formatSeverityTag(s monitor.AlertSeverity) string {
	switch s {
	case monitor.AlertCritical:
		return styleCritical.Render("CRIT")
	case monitor.AlertWarning:
		return styleWarning.Render("WARN")
	case monitor.AlertInfo:
		return styleMuted.Render("INFO")
	default:
		return styleMuted.Render("????")
	}
}
