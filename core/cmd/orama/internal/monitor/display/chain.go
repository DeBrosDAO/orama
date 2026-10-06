package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Chain display constants.
const (
	// shareBarWidth is the width of a validator's voting-power bar.
	shareBarWidth = 20
	// blockAgeWarnSec marks a last block old enough to look at. The chain
	// component counts a node stalled at 60s; this warns before that.
	blockAgeWarnSec = 30
)

// ChainTable prints the Orama L1 as the nodes see it: the chain's height and
// pace, each node's sync, and the validator set.
func ChainTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Chain")
	b.WriteString("\n")
	b.WriteString(ChainTables(t, view.PrepareChain(snap)))
	return flush(w, &b)
}

// ChainTables renders a chain summary: the chain line, the per-node table and
// the validator table.
func ChainTables(t view.Theme, s view.ChainSummary) string {
	if !s.Present {
		return tableIndent + t.Muted.Render("No node reports a chain") + "\n"
	}
	var b strings.Builder
	if s.ChainID == "" {
		b.WriteString(tableIndent + t.Crit.Render("No node's chain RPC answered") + "\n\n")
	} else {
		fmt.Fprintf(&b, "%s%s · height %s · last block %s ago · avg block %.1fs · mempool %d txs\n\n",
			tableIndent, t.Bold.Render(s.ChainID), t.Bold.Render(fmt.Sprint(s.Height)),
			blockAge(t, s.BlockAgeSec), s.AvgBlockTimeSec, s.MempoolTxs)
	}
	rows := make([][]string, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		rows = append(rows, chainRow(t, n))
	}
	b.WriteString(view.Table(t, tableIndent, []string{"NODE", "RPC", "HEIGHT", "BLOCK AGE", "SYNC", "PEERS", "VALIDATOR"}, rows))
	if len(s.Validators) > 0 {
		b.WriteString("\n")
		b.WriteString(validatorTable(t, s))
	}
	return b.String()
}

func chainRow(t view.Theme, n view.ChainNode) []string {
	c := n.Chain
	if !c.Responsive {
		reason := "DOWN"
		if c.Error != "" {
			reason = "DOWN: " + view.Truncate(c.Error, maxErrorChars)
		}
		return []string{n.Host, t.Crit.Render(reason)}
	}
	sync := t.OK.Render("in sync")
	if c.CatchingUp {
		sync = t.Warn.Render("catching up")
	}
	validator := t.Muted.Render("no")
	if c.IsValidator {
		validator = fmt.Sprintf("yes (power %d)", c.VotingPower)
	}
	return []string{n.Host, t.OK.Render("OK"), fmt.Sprint(c.LatestHeight), blockAge(t, c.BlockAgeSec),
		sync, fmt.Sprint(c.Peers), validator}
}

func validatorTable(t view.Theme, s view.ChainSummary) string {
	rows := make([][]string, 0, len(s.Validators))
	for _, v := range s.Validators {
		rows = append(rows, []string{v.Address, fmt.Sprint(v.VotingPower),
			fmt.Sprintf("%s %5.1f%%", view.Bar(v.SharePct, shareBarWidth), v.SharePct)})
	}
	return view.Table(t, tableIndent, []string{"VALIDATOR", "POWER", "SHARE"}, rows)
}

func blockAge(t view.Theme, sec float64) string {
	s := fmt.Sprintf("%.0fs", sec)
	if sec >= blockAgeWarnSec {
		return t.Warn.Render(s)
	}
	return s
}

// ChainJSON writes each node's chain report and the chain-wide summary.
func ChainJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	s := view.PrepareChain(snap)
	type nodeEntry struct {
		Host  string              `json:"host"`
		Chain *report.ChainReport `json:"chain"`
	}
	type validator struct {
		Address     string  `json:"address"`
		VotingPower int64   `json:"voting_power"`
		SharePct    float64 `json:"share_pct"`
	}
	out := struct {
		ChainID         string      `json:"chain_id,omitempty"`
		Height          int64       `json:"height"`
		BlockAgeSec     float64     `json:"block_age_sec"`
		AvgBlockTimeSec float64     `json:"avg_block_time_sec"`
		MempoolTxs      int         `json:"mempool_txs"`
		Nodes           []nodeEntry `json:"nodes"`
		Validators      []validator `json:"validators"`
	}{ChainID: s.ChainID, Height: s.Height, BlockAgeSec: s.BlockAgeSec, AvgBlockTimeSec: s.AvgBlockTimeSec,
		MempoolTxs: s.MempoolTxs, Nodes: []nodeEntry{}, Validators: []validator{}}
	for _, n := range s.Nodes {
		out.Nodes = append(out.Nodes, nodeEntry{Host: n.Host, Chain: n.Chain})
	}
	for _, v := range s.Validators {
		out.Validators = append(out.Validators, validator(v))
	}
	return writeJSON(w, out)
}
