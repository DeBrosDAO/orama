package view

import (
	"sort"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// SeverityFilter narrows an alert list. The empty filter shows everything.
type SeverityFilter string

const (
	FilterAll      SeverityFilter = ""
	FilterCritical SeverityFilter = SeverityFilter(cluster.AlertCritical)
	FilterWarning  SeverityFilter = SeverityFilter(cluster.AlertWarning)
	FilterInfo     SeverityFilter = SeverityFilter(cluster.AlertInfo)
)

// Label names the filter for a status line.
func (f SeverityFilter) Label() string {
	if f == FilterAll {
		return "all"
	}
	return string(f)
}

// AlertRow is one distinct alert and how many times it was raised.
type AlertRow struct {
	cluster.Alert
	Count int
}

// SeverityRank orders severities, most severe first.
func SeverityRank(s cluster.AlertSeverity) int {
	switch s {
	case cluster.AlertCritical:
		return 0
	case cluster.AlertWarning:
		return 1
	case cluster.AlertInfo:
		return 2
	default:
		return 3
	}
}

// PrepareAlerts dedupes identical alerts, keeps those the filter admits, and
// sorts them most severe first, then by subsystem, node and message so the
// order is stable between refreshes.
func PrepareAlerts(alerts []cluster.Alert, filter SeverityFilter) []AlertRow {
	index := map[cluster.Alert]int{}
	var rows []AlertRow
	for _, a := range alerts {
		if filter != FilterAll && string(a.Severity) != string(filter) {
			continue
		}
		if i, seen := index[a]; seen {
			rows[i].Count++
			continue
		}
		index[a] = len(rows)
		rows = append(rows, AlertRow{Alert: a, Count: 1})
	}
	sort.SliceStable(rows, func(i, j int) bool { return alertLess(rows[i].Alert, rows[j].Alert) })
	return rows
}

func alertLess(a, b cluster.Alert) bool {
	if ra, rb := SeverityRank(a.Severity), SeverityRank(b.Severity); ra != rb {
		return ra < rb
	}
	if a.Subsystem != b.Subsystem {
		return a.Subsystem < b.Subsystem
	}
	if a.Node != b.Node {
		return a.Node < b.Node
	}
	return a.Message < b.Message
}

// SeverityTag is the fixed-width label for a severity.
func SeverityTag(s cluster.AlertSeverity) string {
	switch s {
	case cluster.AlertCritical:
		return "CRIT"
	case cluster.AlertWarning:
		return "WARN"
	case cluster.AlertInfo:
		return "INFO"
	default:
		return "????"
	}
}

// NodeLabel is the node an alert names, or "cluster" for a cluster-wide one.
func NodeLabel(a cluster.Alert) string {
	if a.Node == "" {
		return "cluster"
	}
	return a.Node
}
