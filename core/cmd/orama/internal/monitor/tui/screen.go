package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
)

// errSourceStopped is the link error when the source ends without saying why.
var errSourceStopped = errors.New("the monitor source stopped")

func (m model) View() string {
	return strings.Join([]string{
		m.titleLine(),
		m.verdictLine(),
		renderTabBar(m.theme, m.tab, m.width),
		m.viewport.View(),
		m.footer(),
	}, "\n")
}

// titleLine names the environment and source, and says how the view is
// connected, with any error the gateway reported.
func (m model) titleLine() string {
	title := m.theme.Bold.Render(fmt.Sprintf("Orama Monitor · %s", m.cfg.Env))
	parts := []string{title, linkLabel(m.theme, m.cfg.Source.Mode(), m.link, m.now())}
	if m.refreshing {
		parts = append(parts, m.theme.Muted.Render("refreshing…"))
	}
	if m.notice != nil {
		parts = append(parts, m.theme.Warn.Render("! "+errText(m.notice)))
	}
	return strings.Join(parts, "  ")
}

// verdictLine is the verdict on the snapshot on screen, its age marked stale
// when it may no longer be true.
func (m model) verdictLine() string {
	if m.snap == nil {
		return m.theme.Muted.Render("Waiting for the first snapshot…")
	}
	_, v := view.Verdict(m.snap)
	age := m.now().Sub(m.receivedAt)
	stale := isStale(age, m.cfg.Interval, m.cfg.Source.Mode(), m.link.state)
	return view.VerdictLine(m.theme, v, age, stale)
}

func (m model) footer() string {
	hint := "tab/1-9 switch · ↑↓ move · r refresh · ? help · q quit"
	switch {
	case m.tab == tabNodes && m.nodeDetail:
		hint = "esc back · " + hint
	case m.tab == tabNodes:
		hint = "enter node detail · " + hint
	case m.tab == tabAlerts:
		hint = fmt.Sprintf("filter: %s (c/w/i/a) · %s", m.alertFilter.Label(), hint)
	}
	return m.theme.Muted.Render(hint)
}

// render puts the active tab, or the help, into the viewport.
func (m *model) render() {
	width := m.width
	if width == 0 {
		width = view.DefaultWidth
	}
	content := m.tabContent(width)
	if m.showHelp {
		content = helpContent(m.theme)
	}
	// The viewport wraps a line wider than itself, which would break every
	// table; cut lines at the edge instead.
	m.viewport.SetContent(lipgloss.NewStyle().MaxWidth(width).Render(content))
}

// helpContent lists every key binding.
func helpContent(t view.Theme) string {
	var b strings.Builder
	b.WriteString(t.Bold.Render("Keys") + "\n\n")
	for _, k := range keys.helpBindings() {
		h := k.Help()
		fmt.Fprintf(&b, "  %s %s\n", view.PadRight(t.Header.Render(h.Key), helpKeyWidth), h.Desc)
	}
	b.WriteString("\n" + t.Muted.Render("Press ? or esc to close.") + "\n")
	return b.String()
}

// helpKeyWidth aligns the help overlay's descriptions.
const helpKeyWidth = 12

// noSnapshot is what a tab shows before the first snapshot arrives.
func (m model) noSnapshot() string {
	if m.link.state == monitor.LinkFailed {
		return m.theme.Crit.Render("No data: "+errText(m.link.err)) + "\n\n" +
			m.theme.Muted.Render("If the gateways are down, run the monitor with --ssh to read the nodes directly.")
	}
	return m.theme.Muted.Render("Collecting cluster data…")
}
