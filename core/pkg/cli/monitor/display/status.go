package display

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
)

// StatusTable prints the one-line-per-node health summary behind `orama status`.
// It is the shortest view of a snapshot: which nodes are serving, and why the
// rest are not. ClusterTable is the same collection rendered with the numbers.
func StatusTable(snap *monitor.ClusterSnapshot, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "IP\tROLE\tSTATUS\tDETAILS\n")

	healthy := 0
	for _, cs := range snap.Nodes {
		health := cs.Health()
		if health == monitor.HealthHealthy {
			healthy++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", cs.Node.Host, cs.Node.Role, health, cs.Detail())
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write status table: %w", err)
	}

	fmt.Fprintf(w, "\n%d/%d nodes healthy\n", healthy, snap.TotalCount())
	return nil
}

// StatusJSON writes the same summary as machine-readable JSON.
func StatusJSON(snap *monitor.ClusterSnapshot, w io.Writer) error {
	type entry struct {
		Host   string `json:"host"`
		Role   string `json:"role"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}

	entries := make([]entry, 0, len(snap.Nodes))
	for _, cs := range snap.Nodes {
		entries = append(entries, entry{
			Host:   cs.Node.Host,
			Role:   cs.Node.Role,
			Status: string(cs.Health()),
			Error:  cs.Detail(),
		})
	}
	return writeJSON(w, entries)
}
