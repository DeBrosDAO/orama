package display

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))
	styleRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	styleYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffff00"))
	styleMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff"))
)

// statusIcon returns a green "OK" or red "!!" indicator.
func statusIcon(ok bool) string {
	if ok {
		return styleGreen.Render("OK")
	}
	return styleRed.Render("!!")
}

// severityColor returns the lipgloss style for a given alert severity.
func severityColor(s monitor.AlertSeverity) lipgloss.Style {
	switch s {
	case monitor.AlertCritical:
		return styleRed
	case monitor.AlertWarning:
		return styleYellow
	case monitor.AlertInfo:
		return styleMuted
	default:
		return styleMuted
	}
}

// separator returns a dashed line of the given width.
func separator(width int) string {
	return strings.Repeat("\u2500", width)
}

// writeJSON encodes v as indented JSON to w.
func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
