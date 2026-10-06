// Package view turns a cluster snapshot into what the monitor shows: the
// verdict line, tables padded by visible width, alert lists, traffic and chain
// summaries. It is pure data preparation shared by the one-shot tables and the
// live TUI, so both say the same thing about the same snapshot.
package view

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// noColorEnv is the environment variable (https://no-color.org) that turns
// color off whenever it is set to anything non-empty.
const noColorEnv = "NO_COLOR"

// Palette colors. One place, so the tables and the TUI agree.
const (
	colorGreen  = lipgloss.Color("#3fb950")
	colorYellow = lipgloss.Color("#d29922")
	colorRed    = lipgloss.Color("#f85149")
	colorMuted  = lipgloss.Color("#8b949e")
	colorWhite  = lipgloss.Color("#f0f6fc")
	colorTabBg  = lipgloss.Color("#30363d")
)

// Theme is the set of styles one output uses. A plain theme renders every
// string unchanged, which is what a pipe, a file or NO_COLOR asks for.
type Theme struct {
	Color       bool
	OK          lipgloss.Style
	Warn        lipgloss.Style
	Crit        lipgloss.Style
	Muted       lipgloss.Style
	Bold        lipgloss.Style
	Header      lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
}

// NewTheme builds the colored theme, or the plain one when color is false.
func NewTheme(color bool) Theme {
	if !color {
		plain := lipgloss.NewStyle()
		return Theme{
			OK: plain, Warn: plain, Crit: plain, Muted: plain, Bold: plain, Header: plain,
			TabActive: plain.Padding(0, 1), TabInactive: plain.Padding(0, 1),
		}
	}
	return Theme{
		Color:       true,
		OK:          lipgloss.NewStyle().Foreground(colorGreen),
		Warn:        lipgloss.NewStyle().Foreground(colorYellow),
		Crit:        lipgloss.NewStyle().Foreground(colorRed),
		Muted:       lipgloss.NewStyle().Foreground(colorMuted),
		Bold:        lipgloss.NewStyle().Bold(true),
		Header:      lipgloss.NewStyle().Bold(true).Foreground(colorWhite),
		TabActive:   lipgloss.NewStyle().Bold(true).Foreground(colorWhite).Background(colorTabBg).Padding(0, 1),
		TabInactive: lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1),
	}
}

// ThemeFor is the theme for output written to w: colored only on a terminal
// and only when NO_COLOR is unset.
func ThemeFor(w io.Writer) Theme {
	return NewTheme(ColorEnabled(w))
}

// ColorEnabled reports whether output to w may carry ANSI codes.
func ColorEnabled(w io.Writer) bool {
	f, ok := w.(*os.File)
	tty := ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
	return colorAllowed(os.Getenv(noColorEnv), tty)
}

// colorAllowed is the decision behind ColorEnabled: never when NO_COLOR is
// set, and never when the output is not a terminal.
func colorAllowed(noColor string, tty bool) bool {
	return noColor == "" && tty
}

// State styles a component or cluster state.
func (t Theme) State(s cluster.State) lipgloss.Style {
	switch s {
	case cluster.StateOperational:
		return t.OK
	case cluster.StateDegraded:
		return t.Warn
	case cluster.StateOutage:
		return t.Crit
	default:
		return t.Muted
	}
}

// Severity styles an alert severity.
func (t Theme) Severity(s cluster.AlertSeverity) lipgloss.Style {
	switch s {
	case cluster.AlertCritical:
		return t.Crit
	case cluster.AlertWarning:
		return t.Warn
	default:
		return t.Muted
	}
}

// Percent thresholds for resource usage coloring.
const (
	pctWarn = 75
	pctCrit = 90
)

// Pct styles a usage percentage by threshold.
func (t Theme) Pct(pct int) lipgloss.Style {
	switch {
	case pct >= pctCrit:
		return t.Crit
	case pct >= pctWarn:
		return t.Warn
	default:
		return t.OK
	}
}

// Bool renders ok as a green "OK" and a failure as a red label.
func (t Theme) Bool(ok bool, bad string) string {
	if ok {
		return t.OK.Render("OK")
	}
	return t.Crit.Render(bad)
}
