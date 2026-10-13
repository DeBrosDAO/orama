package view

import (
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// ChainNode is one node's view of the chain.
type ChainNode struct {
	Host  string
	Chain *report.ChainReport
}

// ValidatorShare is one validator and its share of the voting power.
type ValidatorShare struct {
	Address     string
	VotingPower int64
	SharePct    float64
}

// ChainSummary is what the chain views show. The chain-wide numbers come from
// the node furthest ahead that answered, since a node catching up reports a
// height and block age that describe itself rather than the chain.
type ChainSummary struct {
	// Present is whether any node reported on a chain at all.
	Present         bool
	ChainID         string
	Height          int64
	BlockAgeSec     float64
	AvgBlockTimeSec float64
	MempoolTxs      int
	Nodes           []ChainNode
	Validators      []ValidatorShare
	TotalPower      int64
}

// PrepareChain summarises the chain across the snapshot's nodes.
func PrepareChain(snap *cluster.ClusterSnapshot) ChainSummary {
	var s ChainSummary
	var best *report.ChainReport
	for _, n := range snap.Nodes {
		if n.Report == nil || n.Report.Chain == nil {
			continue
		}
		c := n.Report.Chain
		s.Present = true
		s.Nodes = append(s.Nodes, ChainNode{Host: n.Node.Host, Chain: c})
		if c.Responsive && (best == nil || c.LatestHeight > best.LatestHeight) {
			best = c
		}
	}
	if best == nil {
		return s
	}
	s.ChainID = best.ChainID
	s.Height = best.LatestHeight
	s.BlockAgeSec = best.BlockAgeSec
	s.AvgBlockTimeSec = best.AvgBlockTimeSec
	s.MempoolTxs = best.MempoolTxs
	s.TotalPower = best.TotalVotingPower
	s.Validators = validatorShares(best.Validators, best.TotalVotingPower)
	return s
}

// validatorShares is the validator set, largest voting power first.
func validatorShares(vals []report.ChainValidator, total int64) []ValidatorShare {
	out := make([]ValidatorShare, 0, len(vals))
	for _, v := range vals {
		share := 0.0
		if total > 0 {
			share = float64(v.VotingPower) * 100 / float64(total)
		}
		out = append(out, ValidatorShare{Address: v.Address, VotingPower: v.VotingPower, SharePct: share})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].VotingPower != out[j].VotingPower {
			return out[i].VotingPower > out[j].VotingPower
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// Bar draws pct (0–100) as a bar width cells wide.
func Bar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	pct = min(max(pct, 0), 100)
	filled := int(pct/100*float64(width) + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
