package display

import (
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// AlertsTable prints every distinct alert, most severe first, each critical
// and warning one with what to do next.
func AlertsTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Alerts")
	rows := view.PrepareAlerts(snap.Alerts, view.FilterAll)
	if len(rows) == 0 {
		fmt.Fprintf(&b, "%s%s\n", tableIndent, t.OK.Render("No alerts"))
		return flush(w, &b)
	}
	b.WriteString(AlertLines(t, rows, snap.Environment, len(rows)))
	return flush(w, &b)
}

// AlertsJSON writes the alerts as JSON, as derived: not deduped or filtered.
func AlertsJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	return writeJSON(w, snap.Alerts)
}

// AlertLines renders up to limit alerts, each critical and warning one with
// its hint, and how many more there are.
func AlertLines(t view.Theme, rows []view.AlertRow, env string, limit int) string {
	var b strings.Builder
	for i, r := range rows {
		if i == limit {
			fmt.Fprintf(&b, "%s%s\n", tableIndent, t.Muted.Render(
				fmt.Sprintf("… %d more: orama monitor alerts --env %s", len(rows)-limit, env)))
			break
		}
		fmt.Fprintf(&b, "%s%s %s %s %s\n", tableIndent,
			t.Severity(r.Severity).Render(view.SeverityTag(r.Severity)),
			view.PadRight(r.Subsystem, subsystemWidth),
			view.PadRight(view.NodeLabel(r.Alert), hostWidth),
			alertMessage(r))
		if r.Severity == cluster.AlertInfo {
			continue
		}
		if hint := view.Hint(r.Alert, env); hint != "" {
			fmt.Fprintf(&b, "%s     %s\n", tableIndent, t.Muted.Render("→ "+hint))
		}
	}
	return b.String()
}

// Column widths for alert lines.
const (
	subsystemWidth = 10
	hostWidth      = 16
)

func alertMessage(r view.AlertRow) string {
	if r.Count > 1 {
		return fmt.Sprintf("%s (×%d)", r.Message, r.Count)
	}
	return r.Message
}
