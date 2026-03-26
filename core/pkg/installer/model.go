package installer

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model is the bubbletea model for the installer
type Model struct {
	step           Step
	config         InstallerConfig
	textInput      textinput.Model
	err            error
	width          int
	height         int
	installing     bool
	installOutput  []string
	cursor         int    // For selection menus
	discovering    bool   // Whether domain discovery is in progress
	discoveryInfo  string // Info message during discovery
	discoveredPeer string // Discovered peer ID from domain
	sniWarning     string // Warning about missing SNI DNS records (non-blocking)
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D4AA")).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			MarginBottom(1)

	focusedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA"))

	blurredStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF6B6B")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00D4AA")).
			Bold(true)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#00D4AA")).
			Padding(1, 2)
)

// NewModel creates a new installer model
func NewModel() Model {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	return Model{
		step:      StepWelcome,
		textInput: ti,
		config: InstallerConfig{
			Branch: "main",
		},
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// installCompleteMsg is sent when installation is complete
type installCompleteMsg struct {
	config InstallerConfig
}

// GetConfig returns the installer configuration after the TUI completes
func (m Model) GetConfig() InstallerConfig {
	return m.config
}
