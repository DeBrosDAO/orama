package tui

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// renderDNSTab renders DNS status for nameserver nodes.
func renderDNSTab(snap *monitor.ClusterSnapshot, width int) string {
	if snap == nil {
		return styleMuted.Render("Collecting cluster data...")
	}

	if snap.HealthyCount() == 0 {
		return styleMuted.Render("No healthy nodes to display.")
	}

	var b strings.Builder

	b.WriteString(styleBold.Render("DNS / Nameserver Status"))
	b.WriteString("\n")
	b.WriteString(separator(width))
	b.WriteString("\n\n")

	hasDNS := false
	for _, cs := range snap.Nodes {
		if cs.Report == nil || cs.Report.DNS == nil {
			continue
		}
		hasDNS = true
		r := cs.Report
		dns := r.DNS
		host := nodeHost(r)
		role := cs.Node.Role

		b.WriteString(styleBold.Render(fmt.Sprintf("  %s", host)))
		if role != "" {
			b.WriteString(fmt.Sprintf("  (%s)", role))
		}
		b.WriteString("\n")

		// Service status
		b.WriteString(fmt.Sprintf("    CoreDNS:       %s", statusStr(dns.CoreDNSActive)))
		if dns.CoreDNSMemMB > 0 {
			b.WriteString(fmt.Sprintf("  mem=%dMB", dns.CoreDNSMemMB))
		}
		if dns.CoreDNSRestarts > 0 {
			b.WriteString(fmt.Sprintf("  restarts=%s", styleWarning.Render(fmt.Sprintf("%d", dns.CoreDNSRestarts))))
		}
		b.WriteString("\n")

		b.WriteString(fmt.Sprintf("    Caddy:         %s\n", statusStr(dns.CaddyActive)))

		// Port bindings
		b.WriteString(fmt.Sprintf("    Ports:         53=%s  80=%s  443=%s\n",
			statusStr(dns.Port53Bound),
			statusStr(dns.Port80Bound),
			statusStr(dns.Port443Bound),
		))

		// DNS resolution checks
		b.WriteString(fmt.Sprintf("    SOA:           %s\n", statusStr(dns.SOAResolves)))
		b.WriteString(fmt.Sprintf("    NS:            %s", statusStr(dns.NSResolves)))
		if dns.NSRecordCount > 0 {
			b.WriteString(fmt.Sprintf("  (%d records)", dns.NSRecordCount))
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("    Base A:        %s\n", statusStr(dns.BaseAResolves)))
		b.WriteString(fmt.Sprintf("    Wildcard:      %s\n", statusStr(dns.WildcardResolves)))
		b.WriteString(fmt.Sprintf("    Corefile:      %s\n", statusStr(dns.CorefileExists)))

		// TLS certificates
		baseTLS := renderTLSDays(dns.BaseTLSDaysLeft, "base")
		wildTLS := renderTLSDays(dns.WildTLSDaysLeft, "wildcard")
		b.WriteString(fmt.Sprintf("    TLS:           %s  %s\n", baseTLS, wildTLS))

		// Log errors
		if dns.LogErrors > 0 {
			b.WriteString(fmt.Sprintf("    Log errors:    %s (5m)\n",
				styleWarning.Render(fmt.Sprintf("%d", dns.LogErrors))))
		}

		b.WriteString("\n")
	}

	if !hasDNS {
		return styleMuted.Render("No nameserver nodes found (no DNS data reported).")
	}

	return b.String()
}

// renderTLSDays formats TLS certificate expiry with color coding.
func renderTLSDays(days int, label string) string {
	if days < 0 {
		return styleMuted.Render(fmt.Sprintf("%s: n/a", label))
	}
	s := fmt.Sprintf("%s: %dd", label, days)
	switch {
	case days < 7:
		return styleCritical.Render(s)
	case days < 14:
		return styleWarning.Render(s)
	default:
		return styleHealthy.Render(s)
	}
}
