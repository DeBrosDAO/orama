package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// labelWidth aligns the subsystem labels of the node summary.
const labelWidth = 11

// NodeTable prints a summary of every subsystem on each node. The live view's
// node detail (Enter on the Nodes tab) shows every field; --json gives all of
// it too.
func NodeTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Nodes")
	for _, cs := range snap.Nodes {
		b.WriteString("\n")
		writeNode(&b, t, cs)
	}
	return flush(w, &b)
}

func writeNode(b *strings.Builder, t view.Theme, cs cluster.CollectionStatus) {
	title := fmt.Sprintf("%s (%s)", cs.Node.Host, cs.Node.Role)
	if cs.Report == nil {
		fmt.Fprintf(b, "%s\n", t.Crit.Render(title))
		reason := cs.Err
		if reason == "" {
			reason = "no report returned"
		}
		line(b, t, "Status", t.Crit.Render("UNREACHABLE: "+reason))
		return
	}
	r := cs.Report
	fmt.Fprintf(b, "%s  %s\n", t.Bold.Render(title), t.Muted.Render("v"+r.Version))
	line(b, t, "System", systemLine(r.System))
	line(b, t, "RQLite", rqliteLine(t, r.RQLite))
	line(b, t, "WireGuard", wireguardLine(t, r.WireGuard))
	line(b, t, "Olric", olricLine(t, r.Olric))
	line(b, t, "IPFS", ipfsLine(t, r.IPFS))
	line(b, t, "Tor", torLine(t, r.Tor))
	line(b, t, "Chain", chainLine(t, r.Chain))
	line(b, t, "Traffic", trafficLine(r.Traffic))
}

func line(b *strings.Builder, t view.Theme, label, value string) {
	if value == "" {
		value = t.Muted.Render("not reported")
	}
	fmt.Fprintf(b, "%s%s %s\n", tableIndent, view.PadRight(label+":", labelWidth), value)
}

func systemLine(s *report.SystemReport) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("CPU %d | Load %.2f | Mem %d%% (%d/%d MB) | Disk %d%%",
		s.CPUCount, s.LoadAvg1, s.MemUsePct, s.MemUsedMB, s.MemTotalMB, s.DiskUsePct)
}

func rqliteLine(t view.Theme, q *report.RQLiteReport) string {
	switch {
	case q == nil:
		return ""
	case !q.Responsive:
		return t.Crit.Render("NOT RESPONDING")
	}
	ready := t.Crit.Render("not ready")
	if q.Ready {
		ready = t.OK.Render("ready")
	}
	return fmt.Sprintf("%s | Term %d | Applied %d | Peers %d | %s", q.RaftState, q.Term, q.Applied, q.NumPeers, ready)
}

func wireguardLine(t view.Theme, wg *report.WireGuardReport) string {
	switch {
	case wg == nil:
		return ""
	case !wg.InterfaceUp:
		return t.Crit.Render("DOWN")
	}
	return fmt.Sprintf("UP | %s | %d peers | handshakes %s", wg.WgIP, wg.PeerCount, t.Bool(handshakesFresh(wg), "STALE"))
}

func olricLine(t view.Theme, o *report.OlricReport) string {
	if o == nil {
		return ""
	}
	return fmt.Sprintf("%s | %d members", activeLabel(t, o.ServiceActive), o.MemberCount)
}

func ipfsLine(t view.Theme, i *report.IPFSReport) string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("%s | %d swarm peers | cluster %s", activeLabel(t, i.DaemonActive), i.SwarmPeerCount, t.Bool(i.ClusterActive, "DOWN"))
}

func chainLine(t view.Theme, c *report.ChainReport) string {
	switch {
	case c == nil:
		return ""
	case !c.Responsive:
		return t.Crit.Render("RPC not answering " + c.Error)
	}
	sync := t.OK.Render("in sync")
	if c.CatchingUp {
		sync = t.Warn.Render("catching up")
	}
	return fmt.Sprintf("%s | height %d | last block %.0fs ago | %s | %d peers", c.ChainID, c.LatestHeight, c.BlockAgeSec, sync, c.Peers)
}

func trafficLine(tr *report.TrafficReport) string {
	if tr == nil {
		return ""
	}
	return fmt.Sprintf("%.1f rps | %.2f%% 5xx | p50 %.0fms p95 %.0fms p99 %.0fms (last %ds)",
		tr.RPS, tr.ErrorRate*100, tr.P50Ms, tr.P95Ms, tr.P99Ms, tr.WindowSec)
}

func activeLabel(t view.Theme, active bool) string {
	if active {
		return t.OK.Render("active")
	}
	return t.Crit.Render("inactive")
}

// NodeJSON writes each node's full report.
func NodeJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	type nodeDetail struct {
		Host   string             `json:"host"`
		Role   string             `json:"role"`
		Status string             `json:"status"`
		Error  string             `json:"error,omitempty"`
		Report *report.NodeReport `json:"report,omitempty"`
	}

	entries := make([]nodeDetail, 0, len(snap.Nodes))
	for _, cs := range snap.Nodes {
		e := nodeDetail{Host: cs.Node.Host, Role: cs.Node.Role, Status: nodeStatusUnknown}
		switch {
		case cs.Err != "":
			e.Status, e.Error = NodeStatusUnreachable, cs.Err
		case cs.Report != nil:
			e.Status, e.Report = NodeStatusOK, cs.Report
		}
		entries = append(entries, e)
	}
	return writeJSON(w, entries)
}
