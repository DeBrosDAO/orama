package view

import (
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// unknownCell fills a cell with no data behind it.
const unknownCell = "--"

// RQLite raft states a settled node reports.
const (
	RaftLeader   = report.RaftLeader
	RaftFollower = report.RaftFollower
)

// NodeHeaders are the columns of the node table.
var NodeHeaders = []string{"HOST", "ROLE", "HEALTH", "RAFT", "GATEWAY", "LOAD", "STEAL", "MEM", "DISK", "VERSION", "AGE"}

// NodeCells renders one node as a row of the node table.
func NodeCells(t Theme, cs cluster.CollectionStatus) []string {
	role := cs.Node.Role
	if role == "" {
		role = "node"
	}
	row := []string{cs.Node.Host, role, healthCell(t, cs.Health())}
	r := cs.Report
	if r == nil {
		return append(row, unknownCell, unknownCell, unknownCell, unknownCell, unknownCell, unknownCell, unknownCell, unknownCell)
	}
	row = append(row, raftCell(t, cs), gatewayCell(t, cs))
	if r.System != nil {
		row = append(row,
			fmt.Sprintf("%.2f", r.System.LoadAvg1),
			t.Pct(int(r.System.CPUStealPct)).Render(fmt.Sprintf("%.0f%%", r.System.CPUStealPct)),
			t.Pct(r.System.MemUsePct).Render(fmt.Sprintf("%d%%", r.System.MemUsePct)),
			t.Pct(r.System.DiskUsePct).Render(fmt.Sprintf("%d%%", r.System.DiskUsePct)))
	} else {
		row = append(row, unknownCell, unknownCell, unknownCell, unknownCell)
	}
	version := r.Version
	if version == "" {
		version = unknownCell
	}
	age := FormatAge(time.Duration(cs.ReportAgeSec) * time.Second)
	return append(row, version, age)
}

func healthCell(t Theme, h cluster.NodeHealth) string {
	switch h {
	case cluster.HealthHealthy:
		return t.OK.Render(string(h))
	case cluster.HealthDegraded:
		return t.Warn.Render(string(h))
	case cluster.HealthUnknown:
		// An older release mid-rollout: not a failure, just not measured.
		return t.Muted.Render(string(h))
	default:
		return t.Crit.Render(string(h))
	}
}

func raftCell(t Theme, cs cluster.CollectionStatus) string {
	q := cs.Report.RQLite
	switch {
	case q == nil:
		return t.Muted.Render(unknownCell)
	case !q.Responsive:
		return t.Crit.Render("DOWN")
	case q.RaftState == RaftLeader || q.RaftState == RaftFollower:
		return t.OK.Render(q.RaftState)
	case q.RaftState == "":
		return t.Warn.Render("unknown")
	default:
		return t.Warn.Render(q.RaftState)
	}
}

func gatewayCell(t Theme, cs cluster.CollectionStatus) string {
	g := cs.Report.Gateway
	switch {
	case g == nil:
		return t.Muted.Render(unknownCell)
	case g.Responsive && g.HTTPStatus == http.StatusOK:
		return t.OK.Render("OK")
	case g.Responsive:
		return t.Warn.Render(fmt.Sprintf("HTTP %d", g.HTTPStatus))
	default:
		return t.Crit.Render("DOWN")
	}
}
