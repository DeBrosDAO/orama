package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
)

var (
	colorGreen  = lipgloss.Color("#00ff00")
	colorRed    = lipgloss.Color("#ff0000")
	colorYellow = lipgloss.Color("#ffff00")
	colorMuted  = lipgloss.Color("#888888")
	colorWhite  = lipgloss.Color("#ffffff")
	colorBg     = lipgloss.Color("#1a1a2e")

	styleHealthy  = lipgloss.NewStyle().Foreground(colorGreen)
	styleWarning  = lipgloss.NewStyle().Foreground(colorYellow)
	styleCritical = lipgloss.NewStyle().Foreground(colorRed)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleBold     = lipgloss.NewStyle().Bold(true)

	activeTab   = lipgloss.NewStyle().Bold(true).Foreground(colorWhite).Background(lipgloss.Color("#333333")).Padding(0, 1)
	inactiveTab = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	footerStyle = lipgloss.NewStyle().Foreground(colorMuted)
)

// statusStr returns a green "OK" when ok is true, red "DOWN" when false.
func statusStr(ok bool) string {
	if ok {
		return styleHealthy.Render("OK")
	}
	return styleCritical.Render("DOWN")
}

// severityStyle returns the appropriate lipgloss style for an alert severity.
func severityStyle(s string) lipgloss.Style {
	switch s {
	case "critical":
		return styleCritical
	case "warning":
		return styleWarning
	case "info":
		return styleMuted
	default:
		return styleMuted
	}
}

// nodeHost returns the best display host for a NodeReport.
func nodeHost(r *report.NodeReport) string {
	if r.PublicIP != "" {
		return r.PublicIP
	}
	return r.Hostname
}
