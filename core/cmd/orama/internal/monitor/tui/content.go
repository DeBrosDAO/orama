package tui

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// tabContent renders the active tab. The tables are the same ones the
// one-shot subcommands print, so the two views never disagree.
func (m model) tabContent(width int) string {
	if m.snap == nil {
		return m.noSnapshot()
	}
	t := m.theme
	switch m.tab {
	case tabOverview:
		return m.overview()
	case tabOperator:
		return section(t, "Your account on the chain", m.operatorContent())
	case tabNodes:
		if m.nodeDetail {
			return m.nodeDetailContent(width)
		}
		return m.nodeTable()
	case tabServices:
		return section(t, "Services by node", display.ServiceMatrix(t, m.snap))
	case tabTraffic:
		return m.traffic()
	case tabChain:
		return section(t, "Orama L1", display.ChainTables(t, view.PrepareChain(m.snap)))
	case tabMesh:
		return section(t, "WireGuard interfaces", display.MeshNodes(t, m.snap)) + "\n" +
			section(t, "Peer links", display.MeshPeers(t, m.snap))
	case tabDNS:
		return section(t, "Nameservers", display.DNSRows(t, m.snap))
	case tabNamespaces:
		return section(t, "Namespaces by node", display.NamespaceRows(t, m.snap))
	case tabAlerts:
		return m.alerts()
	}
	return ""
}

// section is a titled block of a tab.
func section(t view.Theme, title, body string) string {
	return t.Bold.Render(title) + "\n" + body
}

// overview is the component cards and the alerts that need attention first.
func (m model) overview() string {
	t := m.theme
	comps, _ := view.Verdict(m.snap)
	var b strings.Builder
	b.WriteString(section(t, "Components", display.ComponentTable(t, comps)))
	b.WriteString("\n")
	rows := view.PrepareAlerts(m.snap.Alerts, view.FilterAll)
	if len(rows) == 0 {
		b.WriteString(t.OK.Render("No alerts.") + "\n")
		return b.String()
	}
	counts := view.AlertCounts(countSeverity(rows, cluster.AlertCritical), countSeverity(rows, cluster.AlertWarning))
	allHint := fmt.Sprintf(" · all on the Alerts tab (%d)", int(tabAlerts)+1)
	b.WriteString(section(t, "Top alerts "+t.Muted.Render(counts+allHint),
		display.AlertLines(t, rows, m.cfg.Env, display.TopAlertsInOverview)))
	return b.String()
}

func countSeverity(rows []view.AlertRow, severity cluster.AlertSeverity) int {
	n := 0
	for _, r := range rows {
		if r.Severity == severity {
			n += r.Count
		}
	}
	return n
}

// traffic is the traffic tables with the session's cluster-rps sparkline.
func (m model) traffic() string {
	t := m.theme
	var b strings.Builder
	if values := m.rps.Values(); len(values) > 0 {
		peak := 0.0
		for _, v := range values {
			peak = max(peak, v)
		}
		fmt.Fprintf(&b, "%s %s %s\n\n", t.Bold.Render("Cluster rps"), view.Sparkline(values),
			t.Muted.Render(fmt.Sprintf("last %d samples · peak %.1f · now %.1f", len(values), peak, values[len(values)-1])))
	}
	b.WriteString(display.TrafficTables(t, view.AggregateTraffic(m.snap)))
	return b.String()
}

// alerts is every distinct alert the filter admits, most severe first.
func (m model) alerts() string {
	t := m.theme
	rows := view.PrepareAlerts(m.snap.Alerts, m.alertFilter)
	title := fmt.Sprintf("Alerts · showing %s · c critical · w warning · i info · a all", m.alertFilter.Label())
	if len(rows) == 0 {
		return section(t, title, "  "+t.OK.Render("No alerts match."))
	}
	return section(t, title, display.AlertLines(t, rows, m.cfg.Env, len(rows)))
}

// noOperatorHint is the Operator tab when no operator address is known.
const noOperatorHint = "No operator account: pass --operator <orama1...> to orama status."

// operatorContent is the Operator tab: the account's earnings, spendable balance and bond.
func (m model) operatorContent() string {
	if m.cfg.Operator == nil {
		return m.theme.Muted.Render(noOperatorHint) + "\n"
	}
	if m.operator == nil {
		return m.theme.Muted.Render("Reading the account…") + "\n"
	}
	var b strings.Builder
	display.WriteOperator(&b, m.operator)
	return b.String()
}
