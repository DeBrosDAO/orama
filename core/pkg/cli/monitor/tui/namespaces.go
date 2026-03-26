package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderNamespacesTab renders per-namespace health across all nodes.
func renderNamespacesTab(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	reports := snap.Healthy()
	if len(reports) == 0 {
		return styleMuted.Render("No healthy nodes to display.")
	}

	var b strings.Builder

	b.WriteString(styleBold.Render("Namespace Health"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n\n")

	// Collect unique namespace names
	nsSet := make(map[string]bool)
	for _, r := range reports {
		for _, ns := range r.Namespaces {
			nsSet[ns.Name] = true
		}
	}

	nsNames := make([]string, 0, len(nsSet))
	for name := range nsSet {
		nsNames = append(nsNames, name)
	}
	sort.Strings(nsNames)

	if len(nsNames) == 0 {
		return styleMuted.Render("No namespaces found on any node.")
	}

	// Header
	header := fmt.Sprintf("  %-20s", headerStyle.Render("NAMESPACE"))
	for _, r := range reports {
		host := nodeHost(r)
		if len(host) > 15 {
			host = host[:15]
		}
		header += fmt.Sprintf(" %-17s", headerStyle.Render(host))
	}
	b.WriteString(header)
	b.WriteString("\n")

	// Build lookup: host -> ns name -> NamespaceReport
	type nsKey struct {
		host string
		name string
	}
	nsMap := make(map[nsKey]nsStatus)
	for _, r := range reports {
		host := nodeHost(r)
		for _, ns := range r.Namespaces {
			nsMap[nsKey{host, ns.Name}] = nsStatus{
				gateway:     ns.GatewayUp,
				rqlite:      ns.RQLiteUp,
				rqliteState: ns.RQLiteState,
				rqliteReady: ns.RQLiteReady,
				olric:       ns.OlricUp,
			}
		}
	}

	// Rows
	for _, nsName := range nsNames {
		row := fmt.Sprintf("  %-20s", nsName)
		for _, r := range reports {
			host := nodeHost(r)
			ns, ok := nsMap[nsKey{host, nsName}]
			if !ok {
				row += fmt.Sprintf(" %-17s", styleMuted.Render("-"))
				continue
			}
			row += fmt.Sprintf(" %-17s", renderNsCell(ns))
		}
		b.WriteString(row)
		b.WriteString("\n")
	}

	// Detailed per-namespace view
	b.WriteString("\n")
	b.WriteString(styleBold.Render("Namespace Details"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n")

	for _, nsName := range nsNames {
		b.WriteString(fmt.Sprintf("\n  %s\n", styleBold.Render(nsName)))
		for _, r := range reports {
			host := nodeHost(r)
			for _, ns := range r.Namespaces {
				if ns.Name != nsName {
					continue
				}
				b.WriteString(fmt.Sprintf("    %-18s  gw=%s  rqlite=%s",
					host,
					statusStr(ns.GatewayUp),
					statusStr(ns.RQLiteUp),
				))
				if ns.RQLiteState != "" {
					b.WriteString(fmt.Sprintf("(%s)", ns.RQLiteState))
				}
				b.WriteString(fmt.Sprintf("  olric=%s", statusStr(ns.OlricUp)))
				if ns.PortBase > 0 {
					b.WriteString(fmt.Sprintf("  port=%d", ns.PortBase))
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// nsStatus holds a namespace's health indicators for one node.
type nsStatus struct {
	gateway     bool
	rqlite      bool
	rqliteState string
	rqliteReady bool
	olric       bool
}

// renderNsCell renders a compact cell for the namespace matrix.
func renderNsCell(ns nsStatus) string {
	if ns.gateway && ns.rqlite && ns.olric {
		return styleHealthy.Render("OK")
	}
	if !ns.gateway && !ns.rqlite {
		return styleCritical.Render("DOWN")
	}
	// Partial
	parts := []string{}
	if !ns.gateway {
		parts = append(parts, "gw")
	}
	if !ns.rqlite {
		parts = append(parts, "rq")
	}
	if !ns.olric {
		parts = append(parts, "ol")
	}
	return styleWarning.Render("!" + strings.Join(parts, ","))
}
