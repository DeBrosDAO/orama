package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// separatorDot joins the parts of a status line.
const separatorDot = " · "

// Verdict is the cluster's components and one-line verdict for a snapshot.
func Verdict(snap *cluster.ClusterSnapshot) ([]cluster.Component, cluster.Verdict) {
	comps := cluster.Components(snap)
	return comps, cluster.Summarize(snap, comps)
}

// StateIcon is the glyph that leads a state: a check when all is well, a
// cross when something is not, a question mark when nothing is known.
func StateIcon(s cluster.State) string {
	switch s {
	case cluster.StateOperational:
		return "✓"
	case cluster.StateDegraded, cluster.StateOutage:
		return "✗"
	default:
		return "?"
	}
}

// VerdictLine is the line every view starts with, for example
//
//	✓ All systems operational · 3/3 nodes · updated 2s ago
//	✗ Degraded: Database (RQLite) · 2 critical, 1 warning · 2/3 nodes · updated 4s ago
//
// age is how old the snapshot is. A stale snapshot (too old, or from a
// connection that has since dropped) has its age marked STALE, so old data is
// never mistaken for current.
func VerdictLine(t Theme, v cluster.Verdict, age time.Duration, stale bool) string {
	parts := []string{t.State(v.State).Bold(t.Color).Render(StateIcon(v.State) + " " + v.Headline)}
	if v.State != cluster.StateOperational {
		if counts := AlertCounts(v.Critical, v.Warning); counts != "" {
			parts = append(parts, counts)
		}
	}
	parts = append(parts, fmt.Sprintf("%d/%d nodes", v.NodesHealthy, v.NodesTotal))
	parts = append(parts, ageLabel(t, age, stale))
	return strings.Join(parts, t.Muted.Render(separatorDot))
}

// AlertCounts is "2 critical, 1 warning", naming only the non-zero counts.
func AlertCounts(critical, warning int) string {
	var out []string
	if critical > 0 {
		out = append(out, fmt.Sprintf("%d critical", critical))
	}
	if warning == 1 {
		out = append(out, "1 warning")
	} else if warning > 1 {
		out = append(out, fmt.Sprintf("%d warnings", warning))
	}
	return strings.Join(out, ", ")
}

func ageLabel(t Theme, age time.Duration, stale bool) string {
	label := "updated " + FormatAge(age) + " ago"
	if stale {
		return t.Crit.Bold(t.Color).Render("STALE: " + label)
	}
	return t.Muted.Render(label)
}

// FormatAge renders a duration the way people read an age: "4s", "2m10s",
// "3h5m". A negative age (a clock ahead of ours) reads as zero.
func FormatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// SnapshotAge is how old snap is at now.
func SnapshotAge(snap *cluster.ClusterSnapshot, now time.Time) time.Duration {
	if snap == nil || snap.CollectedAt.IsZero() {
		return 0
	}
	return now.Sub(snap.CollectedAt)
}
