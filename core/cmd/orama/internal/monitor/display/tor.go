package display

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// torLine renders one line for the node's Tor client: unit state, SOCKS
// port, bootstrap, and any Anyone leftovers. Empty when not reported.
func torLine(t view.Theme, tor *report.TorReport) string {
	if tor == nil {
		return ""
	}
	if !tor.ClientActive {
		return t.Crit.Render("inactive")
	}
	parts := []string{t.OK.Render("active")}
	if tor.SocksListening {
		parts = append(parts, "socks up")
	} else {
		parts = append(parts, t.Crit.Render("socks down"))
	}
	switch {
	case tor.Bootstrapped:
		parts = append(parts, t.OK.Render("bootstrapped"))
	case tor.BootstrapPct < 0:
		parts = append(parts, t.Muted.Render("bootstrap unknown"))
	default:
		parts = append(parts, t.Warn.Render(fmt.Sprintf("bootstrap %d%%", tor.BootstrapPct)))
	}
	if tor.LegacyAnyone {
		parts = append(parts, t.Warn.Render("Anyone leftovers"))
	}
	return strings.Join(parts, " | ")
}
