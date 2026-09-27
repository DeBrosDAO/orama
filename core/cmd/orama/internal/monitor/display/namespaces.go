package display

import (
	"io"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// NamespacesTable prints each namespace's services on each node hosting it.
func NamespacesTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Namespaces")
	b.WriteString("\n")
	b.WriteString(NamespaceRows(t, snap))
	return flush(w, &b)
}

// NamespaceRows is a row per namespace per node, sorted by namespace then
// node, or a note that there are none.
func NamespaceRows(t view.Theme, snap *cluster.ClusterSnapshot) string {
	type row struct{ ns, host string }
	var keys []row
	cells := map[row][]string{}
	for _, cs := range reported(snap) {
		for _, ns := range cs.Report.Namespaces {
			k := row{ns.Name, cs.Node.Host}
			rqlite := t.Bool(ns.RQLiteUp, "DOWN")
			if ns.RQLiteUp && ns.RQLiteState != "" {
				rqlite = ns.RQLiteState
			}
			if _, seen := cells[k]; !seen {
				keys = append(keys, k)
			}
			cells[k] = []string{ns.Name, cs.Node.Host, rqlite, t.Bool(ns.OlricUp, "DOWN"), t.Bool(ns.GatewayUp, "DOWN")}
		}
	}
	if len(keys) == 0 {
		return tableIndent + t.Muted.Render("No namespaces found") + "\n"
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ns != keys[j].ns {
			return keys[i].ns < keys[j].ns
		}
		return keys[i].host < keys[j].host
	})
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, cells[k])
	}
	return view.Table(t, tableIndent, []string{"NAMESPACE", "NODE", "RQLITE", "OLRIC", "GATEWAY"}, rows)
}

// NamespacesJSON writes namespace health as JSON.
func NamespacesJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	type nsEntry struct {
		Namespace     string `json:"namespace"`
		Host          string `json:"host"`
		RQLiteUp      bool   `json:"rqlite_up"`
		RQLiteState   string `json:"rqlite_state,omitempty"`
		OlricUp       bool   `json:"olric_up"`
		GatewayUp     bool   `json:"gateway_up"`
		GatewayStatus int    `json:"gateway_status,omitempty"`
	}

	entries := []nsEntry{}
	for _, cs := range reported(snap) {
		for _, ns := range cs.Report.Namespaces {
			entries = append(entries, nsEntry{
				Namespace: ns.Name, Host: cs.Node.Host,
				RQLiteUp: ns.RQLiteUp, RQLiteState: ns.RQLiteState,
				OlricUp: ns.OlricUp, GatewayUp: ns.GatewayUp, GatewayStatus: ns.GatewayStatus,
			})
		}
	}
	return writeJSON(w, entries)
}
