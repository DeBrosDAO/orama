package display

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/operatorview"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// noChain is the CHAIN column of a node that runs no chain (a cluster-only node).
const noChain = "-"

// StatusTable prints `orama status` without a terminal: a row per node (cluster health and the
// node's chain), the verdict, and the operator's own account when op is not nil.
func StatusTable(snap *cluster.ClusterSnapshot, op *operatorview.Summary, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "IP\tROLE\tSTATUS\tCHAIN\tDETAILS\n")
	healthy := 0
	for _, cs := range snap.Nodes {
		health := cs.Health()
		if health == cluster.HealthHealthy {
			healthy++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", cs.Node.Host, cs.Node.Role, health, chainCell(nodeChain(cs)), cs.Detail())
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write status table: %w", err)
	}
	_, verdict := view.Verdict(snap)
	fmt.Fprintf(w, "\n%d/%d nodes healthy · %s %s\n", healthy, snap.TotalCount(), view.StateIcon(verdict.State), verdict.Headline)
	if op != nil {
		WriteOperator(w, op)
	}
	return nil
}

// WriteOperator prints the operator's own account: earnings, spendable balance and bond, or why it
// could not be read.
func WriteOperator(w io.Writer, op *operatorview.Summary) {
	fmt.Fprintf(w, "\nOperator %s\n", op.Address)
	if op.Err != "" {
		fmt.Fprintf(w, "  %s\n", op.Err)
	}
	for _, row := range [][2]string{{"Earnings", op.Earnings}, {"Spendable", op.Spendable}, {"Bonded", op.Bonded}} {
		if row[1] != "" {
			fmt.Fprintf(w, "  %-10s %s norama\n", row[0], row[1])
		}
	}
}

func nodeChain(cs cluster.CollectionStatus) *report.ChainReport {
	if cs.Report == nil {
		return nil
	}
	return cs.Report.Chain
}

// chainCell is a node's chain in a few words: its height and whether it is a validator, or why not.
func chainCell(c *report.ChainReport) string {
	switch {
	case c == nil:
		return noChain
	case !c.Responsive:
		return "not answering"
	case c.CatchingUp:
		return fmt.Sprintf("syncing at %d", c.LatestHeight)
	case c.IsValidator:
		return fmt.Sprintf("%d · validator", c.LatestHeight)
	default:
		return fmt.Sprintf("%d", c.LatestHeight)
	}
}

// statusNode is one node of the JSON status.
type statusNode struct {
	Host   string       `json:"host"`
	Role   string       `json:"role"`
	Status string       `json:"status"`
	Error  string       `json:"error,omitempty"`
	Chain  *statusChain `json:"chain,omitempty"`
}

type statusChain struct {
	ChainID     string `json:"chain_id,omitempty"`
	Height      int64  `json:"height"`
	Responsive  bool   `json:"responsive"`
	CatchingUp  bool   `json:"catching_up"`
	Validator   bool   `json:"validator"`
	VotingPower int64  `json:"voting_power"`
}

// statusDoc is `orama status --json`. Healthy is the one bit a script checks: the verdict is
// operational, every node is healthy, and every node that runs a chain answers and has caught up.
type statusDoc struct {
	Healthy  bool                  `json:"healthy"`
	Verdict  cluster.Verdict       `json:"verdict"`
	Nodes    []statusNode          `json:"nodes"`
	Operator *operatorview.Summary `json:"operator,omitempty"`
}

// StatusJSON writes the same status as machine-readable JSON.
func StatusJSON(snap *cluster.ClusterSnapshot, op *operatorview.Summary, w io.Writer) error {
	_, verdict := view.Verdict(snap)
	doc := statusDoc{Verdict: verdict, Nodes: make([]statusNode, 0, len(snap.Nodes)), Operator: op}
	doc.Healthy = verdict.State == cluster.StateOperational && len(snap.Nodes) > 0
	for _, cs := range snap.Nodes {
		n := statusNode{Host: cs.Node.Host, Role: cs.Node.Role, Status: string(cs.Health()), Error: cs.Detail()}
		if c := nodeChain(cs); c != nil {
			n.Chain = &statusChain{ChainID: c.ChainID, Height: c.LatestHeight, Responsive: c.Responsive,
				CatchingUp: c.CatchingUp, Validator: c.IsValidator, VotingPower: c.VotingPower}
			doc.Healthy = doc.Healthy && c.Responsive && !c.CatchingUp
		}
		doc.Healthy = doc.Healthy && cs.Health() == cluster.HealthHealthy
		doc.Nodes = append(doc.Nodes, n)
	}
	return writeJSON(w, doc)
}
