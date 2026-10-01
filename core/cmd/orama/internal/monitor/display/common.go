// Package display renders a cluster snapshot as the one-shot output of the
// `orama monitor` subcommands: tables for people, JSON for scripts. Every
// table starts with the same verdict line the live view shows.
package display

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// tableIndent starts every table row.
const tableIndent = "  "

// maxErrorChars bounds an error message inside a table.
const maxErrorChars = 60

// writeHeader writes the verdict line and the view's title.
func writeHeader(b *strings.Builder, t view.Theme, snap *cluster.ClusterSnapshot, title string) {
	_, v := view.Verdict(snap)
	b.WriteString(view.VerdictLine(t, v, view.SnapshotAge(snap, time.Now()), false))
	b.WriteString("\n\n")
	fmt.Fprintf(b, "%s\n", t.Bold.Render(fmt.Sprintf("%s — %s", title, envLabel(snap))))
}

func envLabel(snap *cluster.ClusterSnapshot) string {
	if snap.Environment == "" {
		return "active environment"
	}
	return snap.Environment
}

// flush writes the rendered output in one call, so a failed write is reported
// rather than lost among many.
func flush(w io.Writer, b *strings.Builder) error {
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write monitor output: %w", err)
	}
	return nil
}

// writeJSON encodes v as indented JSON to w.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write monitor JSON: %w", err)
	}
	return nil
}

// reported is the snapshot's nodes that sent a report.
func reported(snap *cluster.ClusterSnapshot) []cluster.CollectionStatus {
	var out []cluster.CollectionStatus
	for _, n := range snap.Nodes {
		if n.Report != nil {
			out = append(out, n)
		}
	}
	return out
}

// writeUnreachable lists the nodes whose report did not come back, and why.
func writeUnreachable(b *strings.Builder, t view.Theme, snap *cluster.ClusterSnapshot) {
	failed := snap.Failed()
	if len(failed) == 0 {
		return
	}
	b.WriteString("\n")
	for _, cs := range failed {
		fmt.Fprintf(b, "%s%s %s\n", tableIndent, t.Crit.Render(cs.Node.Host+" unreachable:"),
			view.Truncate(cs.Err, maxErrorChars*2))
	}
}
