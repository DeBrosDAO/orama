package display

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// AlertsTable prints alerts sorted by severity to w.
func AlertsTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	critCount, warnCount := countAlerts(snap.Alerts)

	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("Alerts \u2014 %s (%d critical, %d warning)",
			snap.Environment, critCount, warnCount)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 44))
	fmt.Fprintln(w)

	if len(snap.Alerts) == 0 {
		fmt.Fprintln(w, styleGreen.Render("  No alerts"))
		return nil
	}

	// Sort by severity: critical first, then warning, then info
	sorted := make([]monitor.Alert, len(snap.Alerts))
	copy(sorted, snap.Alerts)
	sort.Slice(sorted, func(i, j int) bool {
		return severityRank(sorted[i].Severity) < severityRank(sorted[j].Severity)
	})

	for _, a := range sorted {
		tag := severityTag(a.Severity)
		node := a.Node
		if node == "" {
			node = "cluster"
		}
		fmt.Fprintf(w, "%s %-18s %-12s %s\n",
			tag, node, a.Subsystem, a.Message)
	}

	return nil
}

// AlertsJSON writes alerts as JSON.
func AlertsJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	return writeJSON(w, snap.Alerts)
}

// severityRank returns a sort rank for severity (lower = higher priority).
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
