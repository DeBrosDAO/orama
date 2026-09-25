package display

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/report"
)

// torSummary renders one line for the node's Tor client: unit state, SOCKS
// port, bootstrap, and any Anyone leftovers.
func torSummary(t *report.TorReport) string {
	if !t.ClientActive {
		return styleRed.Render("inactive")
	}
	parts := []string{styleGreen.Render("active")}
	if t.SocksListening {
		parts = append(parts, "socks up")
	} else {
		parts = append(parts, styleRed.Render("socks down"))
	}
	switch {
	case t.Bootstrapped:
		parts = append(parts, styleGreen.Render("bootstrapped"))
	case t.BootstrapPct < 0:
		parts = append(parts, styleMuted.Render("bootstrap unknown"))
	default:
		parts = append(parts, styleYellow.Render(fmt.Sprintf("bootstrap %d%%", t.BootstrapPct)))
	}
	if t.LegacyAnyone {
		parts = append(parts, styleYellow.Render("Anyone leftovers"))
	}
	return strings.Join(parts, " | ")
}
