package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Error-rate thresholds (fraction of requests answered 5xx).
const (
	errorRateWarn = 0.01
	errorRateCrit = 0.05
)

// TrafficTable prints what the gateways served: cluster totals, each node,
// and the busiest namespaces.
func TrafficTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Traffic")
	b.WriteString("\n")
	b.WriteString(TrafficTables(t, view.AggregateTraffic(snap)))
	return flush(w, &b)
}

// TrafficTables renders a traffic summary: the totals line, the node table
// and the namespace table.
func TrafficTables(t view.Theme, s view.TrafficSummary) string {
	if s.Totals.Reporting == 0 {
		return tableIndent + t.Muted.Render("No gateway reported traffic (the SSH source never does: traffic is counted by the gateways)") + "\n"
	}
	var b strings.Builder
	tot := s.Totals
	fmt.Fprintf(&b, "%sCluster: %s · %s 5xx · p50 ≤%.0fms p95 ≤%.0fms p99 ≤%.0fms · %d/%d gateways reporting\n\n",
		tableIndent, t.Bold.Render(fmt.Sprintf("%.1f rps", tot.RPS)), errorRate(t, tot.ErrorRate),
		tot.P50Ms, tot.P95Ms, tot.P99Ms, tot.Reporting, len(s.Nodes))
	var rows [][]string
	for _, n := range s.Nodes {
		rows = append(rows, trafficRow(t, n))
	}
	b.WriteString(view.Table(t, tableIndent, []string{"NODE", "RPS", "5XX", "4XX", "P50", "P95", "P99", "REQUESTS", "WINDOW"}, rows))
	if len(s.Namespaces) > 0 {
		b.WriteString("\n")
		b.WriteString(namespaceTraffic(t, s.Namespaces))
	}
	return b.String()
}

func trafficRow(t view.Theme, n view.NodeTraffic) []string {
	tr := n.Traffic
	if tr == nil {
		none := t.Muted.Render("--")
		return []string{n.Host, none, none, none, none, none, none, none, none}
	}
	return []string{n.Host, fmt.Sprintf("%.1f", tr.RPS), errorRate(t, tr.ErrorRate), fmt.Sprint(tr.Errors4xx),
		fmt.Sprintf("%.0fms", tr.P50Ms), fmt.Sprintf("%.0fms", tr.P95Ms), fmt.Sprintf("%.0fms", tr.P99Ms),
		fmt.Sprint(tr.Requests), fmt.Sprintf("%ds", tr.WindowSec)}
}

func namespaceTraffic(t view.Theme, ns []report.NamespaceTraffic) string {
	rows := make([][]string, 0, len(ns))
	for _, n := range ns {
		rows = append(rows, []string{n.Namespace, fmt.Sprintf("%.1f", n.RPS), fmt.Sprint(n.Requests),
			fmt.Sprint(n.Errors5xx), fmt.Sprintf("%.0fms", n.P95Ms)})
	}
	return view.Table(t, tableIndent, []string{"NAMESPACE", "RPS", "REQUESTS", "5XX", "P95"}, rows)
}

// errorRate renders a 5xx fraction as a percentage, colored by threshold.
func errorRate(t view.Theme, rate float64) string {
	s := fmt.Sprintf("%.2f%%", rate*100)
	switch {
	case rate >= errorRateCrit:
		return t.Crit.Render(s)
	case rate >= errorRateWarn:
		return t.Warn.Render(s)
	default:
		return t.OK.Render(s)
	}
}

// TrafficJSON writes the traffic summary as JSON.
func TrafficJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	s := view.AggregateTraffic(snap)
	type nodeEntry struct {
		Host    string                `json:"host"`
		Traffic *report.TrafficReport `json:"traffic,omitempty"`
	}
	out := struct {
		Totals     trafficTotalsJSON         `json:"totals"`
		Nodes      []nodeEntry               `json:"nodes"`
		Namespaces []report.NamespaceTraffic `json:"namespaces"`
	}{Totals: trafficTotalsJSON(s.Totals), Nodes: []nodeEntry{}, Namespaces: s.Namespaces}
	for _, n := range s.Nodes {
		out.Nodes = append(out.Nodes, nodeEntry{Host: n.Host, Traffic: n.Traffic})
	}
	if out.Namespaces == nil {
		out.Namespaces = []report.NamespaceTraffic{}
	}
	return writeJSON(w, out)
}

// trafficTotalsJSON is view.TrafficTotals with JSON names.
type trafficTotalsJSON struct {
	Reporting int     `json:"reporting_gateways"`
	RPS       float64 `json:"rps"`
	Requests  int64   `json:"requests"`
	Errors5xx int64   `json:"errors_5xx"`
	Errors4xx int64   `json:"errors_4xx"`
	ErrorRate float64 `json:"error_rate"`
	P50Ms     float64 `json:"p50_ms_max"`
	P95Ms     float64 `json:"p95_ms_max"`
	P99Ms     float64 `json:"p99_ms_max"`
}
