package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// handshakeStaleSec is how old a WireGuard handshake may be before the peer
// is shown stale. WireGuard re-handshakes every two minutes on a live tunnel;
// the cluster's mesh alert uses the same bound.
const handshakeStaleSec = 180

// MeshTable prints each node's WireGuard interface, then every peer link.
func MeshTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "WireGuard mesh")
	b.WriteString("\n")
	b.WriteString(MeshNodes(t, snap))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", t.Bold.Render("Peers"))
	b.WriteString(MeshPeers(t, snap))
	return flush(w, &b)
}

// MeshNodes is a row per node: its overlay address, port, peer count against
// the N-1 a full mesh needs (every member, including ones that did not
// report, since a node keeps its peers while one of them is down), and
// whether every handshake is fresh.
func MeshNodes(t view.Theme, snap *cluster.ClusterSnapshot) string {
	expected := snap.TotalCount() - 1
	var rows [][]string
	for _, cs := range reported(snap) {
		wg := cs.Report.WireGuard
		if wg == nil {
			rows = append(rows, []string{cs.Node.Host, t.Muted.Render("no WireGuard"), "", "", ""})
			continue
		}
		status := t.Bool(handshakesFresh(wg), "STALE")
		if !wg.InterfaceUp {
			status = t.Crit.Render("DOWN")
		}
		peers := fmt.Sprintf("%d/%d", wg.PeerCount, expected)
		if wg.PeerCount != expected {
			peers = t.Warn.Render(peers)
		}
		rows = append(rows, []string{cs.Node.Host, wg.WgIP, fmt.Sprint(wg.ListenPort), peers, status})
	}
	return view.Table(t, tableIndent, []string{"NODE", "WG IP", "PORT", "PEERS", "STATUS"}, rows)
}

// MeshPeers is a row per peer link, with its handshake age and traffic.
func MeshPeers(t view.Theme, snap *cluster.ClusterSnapshot) string {
	var rows [][]string
	for _, cs := range reported(snap) {
		wg := cs.Report.WireGuard
		if wg == nil || !wg.InterfaceUp {
			continue
		}
		for _, p := range wg.Peers {
			rows = append(rows, []string{wg.WgIP, stripCIDR(p.AllowedIPs), handshakeCell(t, p),
				printer.FormatBytes(p.TransferRx), printer.FormatBytes(p.TransferTx)})
		}
	}
	if len(rows) == 0 {
		return tableIndent + t.Muted.Render("no peer links reported") + "\n"
	}
	return view.Table(t, tableIndent, []string{"FROM", "TO", "HANDSHAKE", "RX", "TX"}, rows)
}

func handshakeCell(t view.Theme, p report.WGPeerInfo) string {
	switch {
	case p.LatestHandshake == 0:
		return t.Crit.Render("never")
	case p.HandshakeAgeSec > handshakeStaleSec:
		return t.Warn.Render(formatAgo(p.HandshakeAgeSec))
	default:
		return t.OK.Render(formatAgo(p.HandshakeAgeSec))
	}
}

// handshakesFresh reports whether every peer has handshaked recently.
func handshakesFresh(wg *report.WireGuardReport) bool {
	for _, p := range wg.Peers {
		if p.LatestHandshake == 0 || p.HandshakeAgeSec > handshakeStaleSec {
			return false
		}
	}
	return true
}

func stripCIDR(ip string) string {
	if i := strings.Index(ip, "/"); i > 0 {
		return ip[:i]
	}
	return ip
}

// formatAgo formats seconds as an age: "45s ago", "3m ago", "2h ago".
func formatAgo(sec int64) string {
	const minute, hour = 60, 3600
	switch {
	case sec < minute:
		return fmt.Sprintf("%ds ago", sec)
	case sec < hour:
		return fmt.Sprintf("%dm ago", sec/minute)
	default:
		return fmt.Sprintf("%dh ago", sec/hour)
	}
}

// MeshJSON writes the WireGuard mesh as JSON.
func MeshJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	type peerEntry struct {
		AllowedIPs      string `json:"allowed_ips"`
		HandshakeAgeSec int64  `json:"handshake_age_sec"`
		TransferRxBytes int64  `json:"transfer_rx_bytes"`
		TransferTxBytes int64  `json:"transfer_tx_bytes"`
	}
	type meshEntry struct {
		Host       string      `json:"host"`
		WgIP       string      `json:"wg_ip"`
		ListenPort int         `json:"listen_port"`
		PeerCount  int         `json:"peer_count"`
		Up         bool        `json:"up"`
		Peers      []peerEntry `json:"peers,omitempty"`
	}

	entries := []meshEntry{}
	for _, cs := range reported(snap) {
		wg := cs.Report.WireGuard
		if wg == nil {
			continue
		}
		e := meshEntry{Host: cs.Node.Host, WgIP: wg.WgIP, ListenPort: wg.ListenPort, PeerCount: wg.PeerCount, Up: wg.InterfaceUp}
		for _, p := range wg.Peers {
			e.Peers = append(e.Peers, peerEntry{AllowedIPs: p.AllowedIPs, HandshakeAgeSec: p.HandshakeAgeSec,
				TransferRxBytes: p.TransferRx, TransferTxBytes: p.TransferTx})
		}
		entries = append(entries, e)
	}
	return writeJSON(w, entries)
}
