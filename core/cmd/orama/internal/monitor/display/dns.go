package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// TLS expiry thresholds, in days.
const (
	tlsDaysCrit = 7
	tlsDaysWarn = 30
)

// DNSTable prints DNS and TLS status for the nameserver nodes.
func DNSTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "DNS")
	b.WriteString("\n")
	b.WriteString(DNSRows(t, snap))
	return flush(w, &b)
}

// DNSRows is the nameserver table, or a note that there are none.
func DNSRows(t view.Theme, snap *cluster.ClusterSnapshot) string {
	var rows [][]string
	for _, cs := range snap.Nodes {
		if !cs.Node.IsNameserver() {
			continue
		}
		switch {
		case cs.Report == nil:
			rows = append(rows, []string{cs.Node.Host, t.Crit.Render("UNREACHABLE"), "", "", "", "", "", ""})
		case cs.Report.DNS == nil:
			rows = append(rows, []string{cs.Node.Host, t.Muted.Render("no DNS data"), "", "", "", "", "", ""})
		default:
			d := cs.Report.DNS
			rows = append(rows, []string{cs.Node.Host,
				t.Bool(d.CoreDNSActive, "DOWN"), t.Bool(d.CaddyActive, "DOWN"),
				t.Bool(d.SOAResolves, "FAIL"), t.Bool(d.NSResolves, "FAIL"), t.Bool(d.WildcardResolves, "FAIL"),
				tlsDays(t, d.BaseTLSDaysLeft), tlsDays(t, d.WildTLSDaysLeft)})
		}
	}
	if len(rows) == 0 {
		return tableIndent + t.Muted.Render("No nameserver nodes found") + "\n"
	}
	return view.Table(t, tableIndent,
		[]string{"NODE", "COREDNS", "CADDY", "SOA", "NS", "WILDCARD", "BASE TLS", "WILD TLS"}, rows)
}

// DNSJSON writes DNS status as JSON.
func DNSJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
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

	entries := []dnsEntry{}
	for _, cs := range snap.Nodes {
		if !cs.Node.IsNameserver() {
			continue
		}
		e := dnsEntry{Host: cs.Node.Host, Error: cs.Err}
		if cs.Report != nil && cs.Report.DNS != nil {
			d := cs.Report.DNS
			e.CoreDNSActive, e.CaddyActive = d.CoreDNSActive, d.CaddyActive
			e.SOAResolves, e.NSResolves, e.WildcardResolves = d.SOAResolves, d.NSResolves, d.WildcardResolves
			e.BaseTLSDaysLeft, e.WildTLSDaysLeft = d.BaseTLSDaysLeft, d.WildTLSDaysLeft
		}
		entries = append(entries, e)
	}
	return writeJSON(w, entries)
}

// tlsDays renders days until a certificate expires, colored by urgency.
func tlsDays(t view.Theme, days int) string {
	if days < 0 {
		return t.Muted.Render("--")
	}
	s := fmt.Sprintf("%d days", days)
	switch {
	case days < tlsDaysCrit:
		return t.Crit.Render(s)
	case days < tlsDaysWarn:
		return t.Warn.Render(s)
	default:
		return t.OK.Render(s)
	}
}
