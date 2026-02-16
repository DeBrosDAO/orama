package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// DNSTable prints DNS status for nameserver nodes to w.
func DNSTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	fmt.Fprintf(w, "%s\n", styleBold.Render(
		fmt.Sprintf("DNS Status \u2014 %s", snap.Environment)))
	fmt.Fprintln(w, strings.Repeat("\u2550", 22))
	fmt.Fprintln(w)

	// Header
	fmt.Fprintf(w, "%-18s %-9s %-7s %-5s %-5s %-10s %-10s %s\n",
		styleHeader.Render("NODE"),
		styleHeader.Render("COREDNS"),
		styleHeader.Render("CADDY"),
		styleHeader.Render("SOA"),
		styleHeader.Render("NS"),
		styleHeader.Render("WILDCARD"),
		styleHeader.Render("BASE TLS"),
		styleHeader.Render("WILD TLS"))
	fmt.Fprintln(w, separator(78))

	found := false
	for _, cs := range snap.Nodes {
		// Only show nameserver nodes
		if !cs.Node.IsNameserver() {
			continue
		}
		found = true

		if cs.Error != nil || cs.Report == nil {
			fmt.Fprintf(w, "%-18s %s\n",
				styleRed.Render(cs.Node.Host),
				styleRed.Render("UNREACHABLE"))
			continue
		}

		r := cs.Report
		if r.DNS == nil {
			fmt.Fprintf(w, "%-18s %s\n",
				cs.Node.Host,
				styleMuted.Render("no DNS data"))
			continue
		}

		dns := r.DNS
		fmt.Fprintf(w, "%-18s %-9s %-7s %-5s %-5s %-10s %-10s %s\n",
			cs.Node.Host,
			statusIcon(dns.CoreDNSActive),
			statusIcon(dns.CaddyActive),
			statusIcon(dns.SOAResolves),
			statusIcon(dns.NSResolves),
			statusIcon(dns.WildcardResolves),
			tlsDaysStr(dns.BaseTLSDaysLeft),
			tlsDaysStr(dns.WildTLSDaysLeft))
	}

	if !found {
		fmt.Fprintln(w, styleMuted.Render("  No nameserver nodes found"))
	}

	return nil
}

// DNSJSON writes DNS status as JSON.
func DNSJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type dnsEntry struct {
		Host             string `json:"host"`
		CoreDNSActive    bool   `json:"coredns_active"`
		CaddyActive      bool   `json:"caddy_active"`
		SOAResolves      bool   `json:"soa_resolves"`
		NSResolves       bool   `json:"ns_resolves"`
		WildcardResolves bool   `json:"wildcard_resolves"`
		BaseTLSDaysLeft  int    `json:"base_tls_days_left"`
		WildTLSDaysLeft  int    `json:"wild_tls_days_left"`
		Error            string `json:"error,omitempty"`
	}

	var entries []dnsEntry
	for _, cs := range snap.Nodes {
		if !cs.Node.IsNameserver() {
			continue
		}
		e := dnsEntry{Host: cs.Node.Host}
		if cs.Error != nil {
			e.Error = cs.Error.Error()
			entries = append(entries, e)
			continue
		}
		if cs.Report == nil || cs.Report.DNS == nil {
			entries = append(entries, e)
			continue
		}
		dns := cs.Report.DNS
		e.CoreDNSActive = dns.CoreDNSActive
		e.CaddyActive = dns.CaddyActive
		e.SOAResolves = dns.SOAResolves
		e.NSResolves = dns.NSResolves
		e.WildcardResolves = dns.WildcardResolves
		e.BaseTLSDaysLeft = dns.BaseTLSDaysLeft
		e.WildTLSDaysLeft = dns.WildTLSDaysLeft
		entries = append(entries, e)
	}

	return writeJSON(w, entries)
}

// tlsDaysStr formats TLS days left with appropriate coloring.
func tlsDaysStr(days int) string {
	if days < 0 {
		return styleMuted.Render("--")
	}
	s := fmt.Sprintf("%d days", days)
	switch {
	case days < 7:
		return styleRed.Render(s)
	case days < 30:
		return styleYellow.Render(s)
	default:
		return styleGreen.Render(s)
	}
}
