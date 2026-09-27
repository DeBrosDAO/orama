package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// nodeTableFirstRow is the content line of the first node row: the title,
// then the table header, then the rows.
const nodeTableFirstRow = 2

// cursorMark marks the selected node.
const cursorMark = "▶"

// detailKeyWidth aligns the node detail's field names.
const detailKeyWidth = 28

// nodeTable is the Nodes tab: one row per node, the selected one marked.
func (m model) nodeTable() string {
	t := m.theme
	headers := append([]string{" "}, view.NodeHeaders...)
	rows := make([][]string, 0, len(m.snap.Nodes))
	for i, cs := range m.snap.Nodes {
		mark := " "
		if i == m.nodeCursor {
			mark = t.Bold.Render(cursorMark)
		}
		rows = append(rows, append([]string{mark}, view.NodeCells(t, cs)...))
	}
	title := t.Bold.Render(fmt.Sprintf("Nodes (%d)", len(m.snap.Nodes))) + t.Muted.Render(" · ↑↓ select · enter for everything the node reported")
	return title + "\n" + view.Table(t, "", headers, rows)
}

// nodeDetailContent is every section of the selected node's report, or a
// note that the node has left the snapshot.
func (m model) nodeDetailContent(width int) string {
	t := m.theme
	i := nodeIndex(m.snap, m.selectedHost)
	if i < 0 {
		return t.Warn.Render(fmt.Sprintf("%s is not in the latest snapshot.", m.selectedHost)) + "\n" +
			t.Muted.Render("esc to go back")
	}
	cs := m.snap.Nodes[i]
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", t.Bold.Render(fmt.Sprintf("%s (%s)", cs.Node.Host, roleLabel(cs.Node))), t.Muted.Render("esc to go back"))
	b.WriteString(view.Rule(t, width) + "\n")
	b.WriteString(collectionLines(t, cs))
	for _, s := range view.ReportSections(cs.Report) {
		b.WriteString("\n" + t.Header.Render(strings.ToUpper(s.Title)) + "\n")
		for _, f := range s.Fields {
			fmt.Fprintf(&b, "  %s %s\n", view.PadRight(t.Muted.Render(f[0]), detailKeyWidth), f[1])
		}
		for _, tbl := range s.Tables {
			if tbl.Title != "" {
				fmt.Fprintf(&b, "  %s\n", t.Muted.Render(tbl.Title))
			}
			b.WriteString(view.Table(t, "  ", tbl.Headers, tbl.Rows))
		}
	}
	return b.String()
}

// collectionLines says how the node's report was collected, and why not.
func collectionLines(t view.Theme, cs cluster.CollectionStatus) string {
	health := cs.Health()
	style := t.OK
	switch health {
	case cluster.HealthHealthy:
	case cluster.HealthUnknown:
		style = t.Muted
	default:
		style = t.Crit
	}
	line := fmt.Sprintf("  health %s", style.Render(string(health)))
	if d := cs.Detail(); d != "" {
		line += " — " + d
	}
	out := line + "\n" + fmt.Sprintf("  collected in %s · report age %s",
		(time.Duration(cs.DurationMS)*time.Millisecond).String(),
		view.FormatAge(time.Duration(cs.ReportAgeSec)*time.Second))
	if cs.Retries > 0 {
		out += fmt.Sprintf(" · %d retries", cs.Retries)
	}
	return out + "\n"
}

func roleLabel(n cluster.NodeRef) string {
	if n.Role == "" {
		return "node"
	}
	return n.Role
}

// nodeIndex is the position of host in the snapshot, or -1.
func nodeIndex(snap *cluster.ClusterSnapshot, host string) int {
	if snap == nil || host == "" {
		return -1
	}
	for i, n := range snap.Nodes {
		if n.Node.Host == host {
			return i
		}
	}
	return -1
}

// selectedHostAt is the host at position i, or "" when there is none.
func selectedHostAt(snap *cluster.ClusterSnapshot, i int) string {
	if snap == nil || i < 0 || i >= len(snap.Nodes) {
		return ""
	}
	return snap.Nodes[i].Node.Host
}
