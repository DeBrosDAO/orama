package steps

import (
	"github.com/charmbracelet/lipgloss"
)

// Exported styles used across all steps
var (
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D4AA")).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			MarginBottom(1)

	FocusedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA"))

	BlurredStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	CursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA"))

	HelpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")).
			Bold(true)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA")).
			Bold(true)

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#00D4AA")).
			Padding(1, 2)
)

// Package-level aliases for internal use
var (
	titleStyle    = TitleStyle
	subtitleStyle = SubtitleStyle
	focusedStyle  = FocusedStyle
	blurredStyle  = BlurredStyle
	cursorStyle   = CursorStyle
	helpStyle     = HelpStyle
	errorStyle    = ErrorStyle
	successStyle  = SuccessStyle
	boxStyle      = BoxStyle
)
