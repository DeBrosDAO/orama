package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// TopAlertsInOverview is how many alerts the cluster overview lists before
// pointing at `orama monitor alerts`.
const TopAlertsInOverview = 5

// ClusterTable prints the cluster overview: the verdict, each component's
// state, a row per node, and the most severe alerts.
func ClusterTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Cluster")
	comps, _ := view.Verdict(snap)
	b.WriteString("\n")
	b.WriteString(ComponentTable(t, comps))

	b.WriteString("\n")
	rows := make([][]string, 0, len(snap.Nodes))
	for _, cs := range snap.Nodes {
		rows = append(rows, view.NodeCells(t, cs))
	}
	b.WriteString(view.Table(t, tableIndent, view.NodeHeaders, rows))
	writeUnreachable(&b, t, snap)

	alerts := view.PrepareAlerts(snap.Alerts, view.FilterAll)
	if len(alerts) > 0 {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s\n", t.Bold.Render("Top alerts"))
		b.WriteString(AlertLines(t, alerts, snap.Environment, TopAlertsInOverview))
	}
	return flush(w, &b)
}

// ComponentTable lists each component with its state and how many of the
// nodes running it are healthy.
func ComponentTable(t view.Theme, comps []cluster.Component) string {
	rows := make([][]string, 0, len(comps))
	for _, c := range comps {
		style := t.State(c.State)
		rows = append(rows, []string{
			style.Render(view.StateIcon(c.State) + " " + c.Name),
			style.Render(string(c.State)),
			fmt.Sprintf("%d/%d", c.Healthy, c.Total),
			c.Summary,
		})
	}
	return view.Table(t, tableIndent, []string{"COMPONENT", "STATE", "NODES", "SUMMARY"}, rows)
}

// ClusterJSON writes one entry per node.
func ClusterJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
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

	entries := make([]clusterEntry, 0, len(snap.Nodes))
	for _, cs := range snap.Nodes {
		e := clusterEntry{Host: cs.Node.Host, Role: cs.Node.Role, Status: NodeStatusUnreachable, Error: cs.Err}
		r := cs.Report
		if cs.Err != "" || r == nil {
			entries = append(entries, e)
			continue
		}
		e.Status = NodeStatusOK
		if r.System != nil {
			e.MemPct = r.System.MemUsePct
			e.DiskPct = r.System.DiskUsePct
		}
		if r.RQLite != nil && r.RQLite.Responsive {
			e.RQLite = r.RQLite.RaftState
		}
		e.WGUp = r.WireGuard != nil && r.WireGuard.InterfaceUp
		if r.Services != nil {
			e.Services = fmt.Sprintf("%d/%d", activeServices(r.Services.Services), len(r.Services.Services))
		}
		entries = append(entries, e)
	}
	return writeJSON(w, entries)
}
