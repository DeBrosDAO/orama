// Package installer provides an interactive TUI installer for Orama Network
package installer

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InstallerConfig holds the configuration gathered from the TUI
type InstallerConfig struct {
	VpsIP         string
	Domain        string
	JoinAddress   string
	Peers         []string
	ClusterSecret string
	Branch        string
	IsFirstNode   bool
}

// Step represents a step in the installation wizard
type Step int

const (
	StepWelcome Step = iota
	StepNodeType
	StepVpsIP
	StepDomain
	StepJoinAddress
	StepClusterSecret
	StepBranch
	StepConfirm
	StepInstalling
	StepDone
)

// Model is the bubbletea model for the installer
type Model struct {
	step          Step
	config        InstallerConfig
	textInput     textinput.Model
	err           error
	width         int
	height        int
	installing    bool
	installOutput []string
	cursor        int // For selection menus
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

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.step != StepInstalling {
				return m, tea.Quit
			}

		case "enter":
			return m.handleEnter()

		case "up", "k":
			if m.step == StepNodeType || m.step == StepBranch {
				if m.cursor > 0 {
					m.cursor--
				}
			}

		case "down", "j":
			if m.step == StepNodeType {
				if m.cursor < 1 {
					m.cursor++
				}
			} else if m.step == StepBranch {
				if m.cursor < 1 {
					m.cursor++
				}
			}

		case "esc":
			if m.step > StepWelcome && m.step < StepInstalling {
				m.step--
				m.err = nil
				m.setupStepInput()
			}
		}
	}

	// Update text input for input steps
	if m.step == StepVpsIP || m.step == StepDomain || m.step == StepJoinAddress || m.step == StepClusterSecret {
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.step {
	case StepWelcome:
		m.step = StepNodeType
		m.cursor = 0

	case StepNodeType:
		m.config.IsFirstNode = m.cursor == 0
		m.step = StepVpsIP
		m.setupStepInput()

	case StepVpsIP:
		ip := strings.TrimSpace(m.textInput.Value())
		if err := validateIP(ip); err != nil {
			m.err = err
			return m, nil
		}
		m.config.VpsIP = ip
		m.err = nil
		m.step = StepDomain
		m.setupStepInput()

	case StepDomain:
		domain := strings.TrimSpace(m.textInput.Value())
		if err := validateDomain(domain); err != nil {
			m.err = err
			return m, nil
		}
		m.config.Domain = domain
		m.err = nil
		if m.config.IsFirstNode {
			m.step = StepBranch
			m.cursor = 0
		} else {
			m.step = StepJoinAddress
			m.setupStepInput()
		}

	case StepJoinAddress:
		addr := strings.TrimSpace(m.textInput.Value())
		if addr != "" {
			if err := validateJoinAddress(addr); err != nil {
				m.err = err
				return m, nil
			}
			m.config.JoinAddress = addr
		}
		m.err = nil
		m.step = StepClusterSecret
		m.setupStepInput()

	case StepClusterSecret:
		secret := strings.TrimSpace(m.textInput.Value())
		if err := validateClusterSecret(secret); err != nil {
			m.err = err
			return m, nil
		}
		m.config.ClusterSecret = secret
		m.err = nil
		m.step = StepBranch
		m.cursor = 0

	case StepBranch:
		if m.cursor == 0 {
			m.config.Branch = "main"
		} else {
			m.config.Branch = "nightly"
		}
		m.step = StepConfirm

	case StepConfirm:
		m.step = StepInstalling
		return m, m.startInstallation()

	case StepDone:
		return m, tea.Quit
	}

	return m, nil
}

func (m *Model) setupStepInput() {
	m.textInput.Reset()
	m.textInput.Focus()

	switch m.step {
	case StepVpsIP:
		m.textInput.Placeholder = "e.g., 203.0.113.1"
		// Try to auto-detect public IP
		if ip := detectPublicIP(); ip != "" {
			m.textInput.SetValue(ip)
		}
	case StepDomain:
		m.textInput.Placeholder = "e.g., node-1.orama.network"
	case StepJoinAddress:
		m.textInput.Placeholder = "e.g., 203.0.113.1:7001 (or leave empty)"
	case StepClusterSecret:
		m.textInput.Placeholder = "64 hex characters"
		m.textInput.EchoMode = textinput.EchoPassword
	}
}

func (m Model) startInstallation() tea.Cmd {
	return func() tea.Msg {
		// This would trigger the actual installation
		// For now, we return the config for the CLI to handle
		return installCompleteMsg{config: m.config}
	}
}

type installCompleteMsg struct {
	config InstallerConfig
}

// View renders the UI
func (m Model) View() string {
	var s strings.Builder

	// Header
	s.WriteString(renderHeader())
	s.WriteString("\n\n")

	switch m.step {
	case StepWelcome:
		s.WriteString(m.viewWelcome())
	case StepNodeType:
		s.WriteString(m.viewNodeType())
	case StepVpsIP:
		s.WriteString(m.viewVpsIP())
	case StepDomain:
		s.WriteString(m.viewDomain())
	case StepJoinAddress:
		s.WriteString(m.viewJoinAddress())
	case StepClusterSecret:
		s.WriteString(m.viewClusterSecret())
	case StepBranch:
		s.WriteString(m.viewBranch())
	case StepConfirm:
		s.WriteString(m.viewConfirm())
	case StepInstalling:
		s.WriteString(m.viewInstalling())
	case StepDone:
		s.WriteString(m.viewDone())
	}

	return s.String()
}

func renderHeader() string {
	logo := `
   ___  ____      _    __  __    _    
  / _ \|  _ \    / \  |  \/  |  / \   
 | | | | |_) |  / _ \ | |\/| | / _ \  
 | |_| |  _ <  / ___ \| |  | |/ ___ \ 
  \___/|_| \_\/_/   \_\_|  |_/_/   \_\
`
	return titleStyle.Render(logo) + "\n" + subtitleStyle.Render("Network Installation Wizard")
}

func (m Model) viewWelcome() string {
	var s strings.Builder
	s.WriteString(boxStyle.Render(
		titleStyle.Render("Welcome to Orama Network!") + "\n\n" +
			"This wizard will guide you through setting up your node.\n\n" +
			"You'll need:\n" +
			"  • A public IP address for your server\n" +
			"  • A domain name (e.g., node-1.orama.network)\n" +
			"  • For joining: cluster secret from existing node\n",
	))
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to continue • q to quit"))
	return s.String()
}

func (m Model) viewNodeType() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Node Type") + "\n\n")
	s.WriteString("Is this the first node in a new cluster?\n\n")

	options := []string{"Yes, create new cluster", "No, join existing cluster"}
	for i, opt := range options {
		if i == m.cursor {
			s.WriteString(cursorStyle.Render("→ ") + focusedStyle.Render(opt) + "\n")
		} else {
			s.WriteString("  " + blurredStyle.Render(opt) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to select • Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewVpsIP() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Server IP Address") + "\n\n")
	s.WriteString("Enter your server's public IP address:\n\n")
	s.WriteString(m.textInput.View())

	if m.err != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ " + m.err.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewDomain() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Domain Name") + "\n\n")
	s.WriteString("Enter the domain for this node:\n\n")
	s.WriteString(m.textInput.View())

	if m.err != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ " + m.err.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewJoinAddress() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Join Address") + "\n\n")
	s.WriteString("Enter the RQLite address to join (IP:port):\n")
	s.WriteString(subtitleStyle.Render("Leave empty to auto-detect from peers") + "\n\n")
	s.WriteString(m.textInput.View())

	if m.err != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ " + m.err.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewClusterSecret() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Cluster Secret") + "\n\n")
	s.WriteString("Enter the cluster secret from an existing node:\n")
	s.WriteString(subtitleStyle.Render("Get it with: cat ~/.orama/secrets/cluster-secret") + "\n\n")
	s.WriteString(m.textInput.View())

	if m.err != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ " + m.err.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewBranch() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Release Channel") + "\n\n")
	s.WriteString("Select the release channel:\n\n")

	options := []string{"main (stable)", "nightly (latest features)"}
	for i, opt := range options {
		if i == m.cursor {
			s.WriteString(cursorStyle.Render("→ ") + focusedStyle.Render(opt) + "\n")
		} else {
			s.WriteString("  " + blurredStyle.Render(opt) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to select • Enter to confirm • Esc to go back"))
	return s.String()
}

func (m Model) viewConfirm() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Confirm Installation") + "\n\n")

	config := fmt.Sprintf(
		"  VPS IP:     %s\n"+
			"  Domain:     %s\n"+
			"  Branch:     %s\n"+
			"  Node Type:  %s\n",
		m.config.VpsIP,
		m.config.Domain,
		m.config.Branch,
		map[bool]string{true: "First node (new cluster)", false: "Join existing cluster"}[m.config.IsFirstNode],
	)

	if !m.config.IsFirstNode {
		if m.config.JoinAddress != "" {
			config += fmt.Sprintf("  Join Addr:  %s\n", m.config.JoinAddress)
		}
		config += fmt.Sprintf("  Secret:     %s...\n", m.config.ClusterSecret[:8])
	}

	s.WriteString(boxStyle.Render(config))
	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Press Enter to install • Esc to go back"))
	return s.String()
}

func (m Model) viewInstalling() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Installing...") + "\n\n")
	s.WriteString("Please wait while the node is being configured.\n\n")
	for _, line := range m.installOutput {
		s.WriteString(line + "\n")
	}
	return s.String()
}

func (m Model) viewDone() string {
	var s strings.Builder
	s.WriteString(successStyle.Render("✓ Installation Complete!") + "\n\n")
	s.WriteString("Your node is now running.\n\n")
	s.WriteString("Useful commands:\n")
	s.WriteString("  orama status        - Check service status\n")
	s.WriteString("  orama logs node     - View node logs\n")
	s.WriteString("  orama logs gateway  - View gateway logs\n")
	s.WriteString("\n")
	s.WriteString(helpStyle.Render("Press Enter or q to exit"))
	return s.String()
}

// GetConfig returns the installer configuration after the TUI completes
func (m Model) GetConfig() InstallerConfig {
	return m.config
}

// Validation helpers

func validateIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address is required")
	}
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address format")
	}
	return nil
}

func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain is required")
	}
	// Basic domain validation
	domainRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$`)
	if !domainRegex.MatchString(domain) {
		return fmt.Errorf("invalid domain format")
	}
	return nil
}

func validateJoinAddress(addr string) error {
	if addr == "" {
		return nil // Optional
	}
	_, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address format (expected IP:port)")
	}
	return nil
}

func validateClusterSecret(secret string) error {
	if len(secret) != 64 {
		return fmt.Errorf("cluster secret must be 64 hex characters")
	}
	secretRegex := regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	if !secretRegex.MatchString(secret) {
		return fmt.Errorf("cluster secret must be valid hexadecimal")
	}
	return nil
}

func detectPublicIP() string {
	// Try to detect public IP from common interfaces
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil && !ipnet.IP.IsPrivate() {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

// Run starts the TUI installer and returns the configuration
func Run() (*InstallerConfig, error) {
	// Check if running as root
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("installer must be run as root (use sudo)")
	}

	p := tea.NewProgram(NewModel(), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	m := finalModel.(Model)
	if m.step == StepInstalling || m.step == StepDone {
		config := m.GetConfig()
		return &config, nil
	}

	return nil, fmt.Errorf("installation cancelled")
}

