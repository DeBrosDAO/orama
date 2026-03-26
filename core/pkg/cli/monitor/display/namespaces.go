package display

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// NamespacesTable prints per-namespace health across nodes to w.
func NamespacesTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("Namespace Health \u2014 %s", snap.Environment)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 28))
	fmt.Fprintln(w)

	// Collect all namespace entries across nodes
	type nsRow struct {
		namespace string
		host      string
		rqlite    string
		olric     string
		gateway   string
	}

	var rows []nsRow
	nsNames := map[string]bool{}

	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil {
			continue
		}
		for _, ns := range cs.Report.Namespaces {
			nsNames[ns.Name] = true

			rqliteStr := statusIcon(ns.RQLiteUp)
			if ns.RQLiteUp && ns.RQLiteState != "" {
				rqliteStr = ns.RQLiteState
			}

			rows = append(rows, nsRow{
				namespace: ns.Name,
				host:      cs.Node.Host,
				rqlite:    rqliteStr,
				olric:     statusIcon(ns.OlricUp),
				gateway:   statusIcon(ns.GatewayUp),
			})
		}
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, styleMuted.Render("  No namespaces found"))
		return nil
	}

	// Sort by namespace name, then host
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].namespace != rows[j].namespace {
			return rows[i].namespace < rows[j].namespace
		}
		return rows[i].host < rows[j].host
	})

	// Header
	fmt.Fprintf(w, "%-13s %-18s %-11s %-7s %s\n",
		styleHeader.Render("NAMESPACE"),
		styleHeader.Render("NODE"),
		styleHeader.Render("RQLITE"),
		styleHeader.Render("OLRIC"),
		styleHeader.Render("GATEWAY"))
	fmt.Fprintln(w, separator(58))

	for _, r := range rows {
		fmt.Fprintf(w, "%-13s %-18s %-11s %-7s %s\n",
			r.namespace, r.host, r.rqlite, r.olric, r.gateway)
	}

	return nil
}

// NamespacesJSON writes namespace health as JSON.
func NamespacesJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type nsEntry struct {
		Namespace     string `json:"namespace"`
		Host          string `json:"host"`
		RQLiteUp      bool   `json:"rqlite_up"`
		RQLiteState   string `json:"rqlite_state,omitempty"`
		OlricUp       bool   `json:"olric_up"`
		GatewayUp     bool   `json:"gateway_up"`
		GatewayStatus int    `json:"gateway_status,omitempty"`
	}

	var entries []nsEntry
	for _, cs := range snap.Nodes {
		if cs.Error != nil || cs.Report == nil {
			continue
		}
		for _, ns := range cs.Report.Namespaces {
			entries = append(entries, nsEntry{
				Namespace:     ns.Name,
				Host:          cs.Node.Host,
				RQLiteUp:      ns.RQLiteUp,
				RQLiteState:   ns.RQLiteState,
				OlricUp:       ns.OlricUp,
				GatewayUp:     ns.GatewayUp,
				GatewayStatus: ns.GatewayStatus,
			})
		}
	}

	return writeJSON(w, entries)
}
