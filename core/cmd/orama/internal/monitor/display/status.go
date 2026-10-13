package display

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/operatorview"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// noChain is the CHAIN column of a node that runs no chain (a cluster-only node).
const noChain = "-"

// StatusTable prints `orama status` without a terminal: a row per node (cluster health, the node's
// chain and its update finding), the verdict, what the update agents found and the chain upgrade
// scheduled, and the operator's own account when op is not nil.
func StatusTable(snap *cluster.ClusterSnapshot, op *operatorview.Summary, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "IP\tROLE\tSTATUS\tCHAIN\tUPDATE\tDETAILS\n")
	healthy := 0
	theme := view.NewTheme(false)
	for _, cs := range snap.Nodes {
		health := cs.Health()
		if health == cluster.HealthHealthy {
			healthy++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", cs.Node.Host, cs.Node.Role, health, chainCell(nodeChain(cs)),
			view.UpdateCell(theme, nodeUpdate(cs)), cs.Detail())
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write status table: %w", err)
	}
	_, verdict := view.Verdict(snap)
	fmt.Fprintf(w, "\n%d/%d nodes healthy · %s %s\n", healthy, snap.TotalCount(), view.StateIcon(verdict.State), verdict.Headline)
	writeUpdates(w, snap)
	if op != nil {
		WriteOperator(w, op)
	}
	return nil
}

// writeUpdates prints, under the table, what each node's auto-update agent found (the release,
// what the policy does with it and why) and the chain upgrade scheduled for each node (its height,
// the blocks and time left, whether the binary is staged). It prints nothing when no node has
// either.
func writeUpdates(w io.Writer, snap *cluster.ClusterSnapshot) {
	var updates, upgrades []string
	for _, cs := range snap.Nodes {
		if sentence := view.UpdateSentence(nodeUpdate(cs)); sentence != "" {
			updates = append(updates, fmt.Sprintf("  %s  %s\n", cs.Node.Host, sentence))
		}
		if sentence := view.UpgradeSentence(nodeChain(cs)); sentence != "" {
			upgrades = append(upgrades, fmt.Sprintf("  %s  %s\n", cs.Node.Host, sentence))
		}
	}
	for _, section := range []struct {
		title string
		lines []string
	}{{"Updates", updates}, {"Scheduled chain upgrade", upgrades}} {
		if len(section.lines) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n%s\n%s", section.title, strings.Join(section.lines, ""))
	}
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
	writeRelays(w, op)
}

// writeRelays prints the Tor relays registered on the operator's nodes and what each was last paid.
// A relay's pay is part of the earnings above: x/relay credits it there, 90% of what it settled.
func writeRelays(w io.Writer, op *operatorview.Summary) {
	if len(op.Relays) == 0 {
		return
	}
	fmt.Fprintf(w, "  Relays     %d registered; their pay is part of the earnings (90%% of each payout, after the burn and the archive share)\n", len(op.Relays))
	for _, r := range op.Relays {
		kind := "relay"
		switch {
		case r.Jailed:
			kind = "relay, jailed: earns nothing"
		case r.Exit:
			kind = "exit relay"
		}
		paid := "never paid"
		if r.LastPayoutEpoch > 0 {
			paid = fmt.Sprintf("last paid %s norama in epoch %d", r.LastPayout, r.LastPayoutEpoch)
		}
		fmt.Fprintf(w, "    %s  node %s  %s  %s\n", r.Fingerprint, r.NodeID, kind, paid)
	}
	if op.RelaysMore {
		fmt.Fprintln(w, "    (more relays than listed here: orama chain query orama.relay.v1.Query/OperatorRelays shows the rest)")
	}
}

func nodeChain(cs cluster.CollectionStatus) *report.ChainReport {
	if cs.Report == nil {
		return nil
	}
	return cs.Report.Chain
}

func nodeUpdate(cs cluster.CollectionStatus) *updatenotice.Notice {
	if cs.Report == nil {
		return nil
	}
	return cs.Report.Update
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
	// Update is what the node's auto-update agent last found; absent when it found nothing.
	Update *statusUpdate `json:"update,omitempty"`
}

// statusUpdate is the agent's finding with what the cluster's policy does with it: notify,
// upgrade, refuse or rolled back (Reason says why).
type statusUpdate struct {
	updatenotice.Notice
	Policy string `json:"policy"`
}

type statusChain struct {
	ChainID     string `json:"chain_id,omitempty"`
	Height      int64  `json:"height"`
	Responsive  bool   `json:"responsive"`
	CatchingUp  bool   `json:"catching_up"`
	Validator   bool   `json:"validator"`
	VotingPower int64  `json:"voting_power"`
	// Upgrade is the chain upgrade scheduled for the node; UpgradeError is why it could not be read.
	Upgrade      *statusUpgrade `json:"upgrade,omitempty"`
	UpgradeError string         `json:"upgrade_error,omitempty"`
}

// statusUpgrade is the chain upgrade scheduled for a node. BlocksRemaining is zero or negative once
// the height has passed; ETASeconds is absent when no block time is known or the height is reached.
type statusUpgrade struct {
	Name            string `json:"name"`
	Height          int64  `json:"height"`
	BlocksRemaining int64  `json:"blocks_remaining"`
	ETASeconds      *int64 `json:"eta_seconds,omitempty"`
	Staged          bool   `json:"staged"`
	StageError      string `json:"stage_error,omitempty"`
	// Checksums is "matches", "mismatch", "unnamed" or "unchecked" once the binary is staged.
	Checksums       string `json:"checksums,omitempty"`
	ChecksumProblem string `json:"checksum_problem,omitempty"`
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
				CatchingUp: c.CatchingUp, Validator: c.IsValidator, VotingPower: c.VotingPower,
				Upgrade: upgradeJSON(c), UpgradeError: c.UpgradeError}
			doc.Healthy = doc.Healthy && c.Responsive && !c.CatchingUp
		}
		if u := nodeUpdate(cs); u != nil {
			n.Update = &statusUpdate{Notice: *u, Policy: u.Policy()}
		}
		doc.Healthy = doc.Healthy && cs.Health() == cluster.HealthHealthy
		doc.Nodes = append(doc.Nodes, n)
	}
	return writeJSON(w, doc)
}

// upgradeJSON is the chain's scheduled upgrade as the JSON status carries it; nil when none is.
func upgradeJSON(c *report.ChainReport) *statusUpgrade {
	u := c.Upgrade
	if u == nil {
		return nil
	}
	cd := view.CountdownTo(c, u)
	out := &statusUpgrade{Name: u.Name, Height: u.Height, BlocksRemaining: cd.BlocksLeft, Staged: u.Staged, StageError: u.StageError,
		Checksums: u.Checksums, ChecksumProblem: u.ChecksumProblem}
	if cd.HasETA {
		eta := int64(cd.ETA.Seconds())
		out.ETASeconds = &eta
	}
	return out
}
