// Package installer provides an interactive TUI installer for Orama Network
package installer

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/pkg/certutil"
	"github.com/DeBrosOfficial/network/pkg/tlsutil"
)

// InstallerConfig holds the configuration gathered from the TUI
type InstallerConfig struct {
	VpsIP          string
	Domain         string
	PeerDomain     string   // Domain of existing node to join
	PeerIP         string   // Resolved IP of peer domain (for Raft join)
	JoinAddress    string   // Auto-populated: {PeerIP}:7002 (direct RQLite TLS)
	Peers          []string // Auto-populated: /dns4/{PeerDomain}/tcp/4001/p2p/{PeerID}
	ClusterSecret  string
	SwarmKeyHex    string   // 64-hex IPFS swarm key (for joining private network)
	IPFSPeerID     string   // IPFS peer ID (auto-discovered from peer domain)
	IPFSSwarmAddrs []string // IPFS swarm addresses (auto-discovered from peer domain)
	Branch         string
	IsFirstNode    bool
	NoPull         bool
}

// Step represents a step in the installation wizard
type Step int

const (
	StepWelcome Step = iota
	StepNodeType
	StepVpsIP
	StepDomain
	StepPeerDomain // Domain of existing node to join (replaces StepJoinAddress)
	StepClusterSecret
	StepSwarmKey // 64-hex swarm key for IPFS private network
	StepBranch
	StepNoPull
	StepConfirm
	StepInstalling
	StepDone
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

	case installCompleteMsg:
		m.step = StepDone
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
		if m.step == StepNodeType || m.step == StepBranch || m.step == StepNoPull {
				if m.cursor > 0 {
					m.cursor--
				}
			}

		case "down", "j":
		if m.step == StepNodeType || m.step == StepBranch || m.step == StepNoPull {
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
	if m.step == StepVpsIP || m.step == StepDomain || m.step == StepPeerDomain || m.step == StepClusterSecret || m.step == StepSwarmKey {
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

		// Check SNI DNS records for this domain
		m.discovering = true
		m.discoveryInfo = "Validating SNI DNS records for " + domain + "..."

		if err := validateSNIDNSRecords(domain); err != nil {
			m.discovering = false
			m.err = fmt.Errorf("SNI DNS validation failed: %w", err)
			return m, nil
		}

		m.discovering = false
		m.config.Domain = domain
		m.err = nil

		// Auto-generate self-signed certificates for this domain
		m.discovering = true
		m.discoveryInfo = "Generating SSL certificates for " + domain + "..."

		if err := ensureCertificatesForDomain(domain); err != nil {
			m.discovering = false
			m.err = fmt.Errorf("failed to generate certificates: %w", err)
			return m, nil
		}

		m.discovering = false

		if m.config.IsFirstNode {
			m.step = StepBranch
			m.cursor = 0
		} else {
			m.step = StepPeerDomain
			m.setupStepInput()
		}

	case StepPeerDomain:
		peerDomain := strings.TrimSpace(m.textInput.Value())
		if err := validateDomain(peerDomain); err != nil {
			m.err = err
			return m, nil
		}

		// Validate SNI DNS records for peer domain
		m.discovering = true
		m.discoveryInfo = "Validating SNI DNS records for " + peerDomain + "..."

		if err := validateSNIDNSRecords(peerDomain); err != nil {
			m.discovering = false
			m.err = fmt.Errorf("SNI DNS validation failed: %w", err)
			return m, nil
		}

		// Discover peer info from domain (try HTTPS first, then HTTP)
		m.discovering = true
		m.discoveryInfo = "Discovering peer from " + peerDomain + "..."

		discovery, err := discoverPeerFromDomain(peerDomain)
		m.discovering = false

		if err != nil {
			m.err = fmt.Errorf("failed to discover peer: %w", err)
			return m, nil
		}

		// Store discovered info
		m.config.PeerDomain = peerDomain
		m.discoveredPeer = discovery.PeerID

		// Resolve peer domain to IP for direct RQLite TLS connection
		// RQLite uses native TLS on port 7002 (not SNI gateway on 7001)
		peerIPs, err := net.LookupIP(peerDomain)
		if err != nil || len(peerIPs) == 0 {
			m.err = fmt.Errorf("failed to resolve peer domain %s to IP: %w", peerDomain, err)
			return m, nil
		}
		// Prefer IPv4
		var peerIP string
		for _, ip := range peerIPs {
			if ip.To4() != nil {
				peerIP = ip.String()
				break
			}
		}
		if peerIP == "" {
			peerIP = peerIPs[0].String()
		}
		m.config.PeerIP = peerIP

		// Auto-populate join address (direct RQLite TLS on port 7002) and bootstrap peers
		m.config.JoinAddress = fmt.Sprintf("%s:7002", peerIP)
		m.config.Peers = []string{
			fmt.Sprintf("/dns4/%s/tcp/4001/p2p/%s", peerDomain, discovery.PeerID),
		}

		// Store IPFS peer info for Peering.Peers configuration
		if discovery.IPFSPeerID != "" {
			m.config.IPFSPeerID = discovery.IPFSPeerID
			m.config.IPFSSwarmAddrs = discovery.IPFSSwarmAddrs
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
		m.step = StepSwarmKey
		m.setupStepInput()

	case StepSwarmKey:
		swarmKey := strings.TrimSpace(m.textInput.Value())
		if err := validateSwarmKey(swarmKey); err != nil {
			m.err = err
			return m, nil
		}
		m.config.SwarmKeyHex = swarmKey
		m.err = nil
		m.step = StepBranch
		m.cursor = 0

	case StepBranch:
		if m.cursor == 0 {
			m.config.Branch = "main"
		} else {
			m.config.Branch = "nightly"
		}
		m.cursor = 0 // Reset cursor for next step
		m.step = StepNoPull

	case StepNoPull:
		if m.cursor == 0 {
			m.config.NoPull = false
		} else {
			m.config.NoPull = true
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
	m.textInput.EchoMode = textinput.EchoNormal // Reset echo mode

	switch m.step {
	case StepVpsIP:
		m.textInput.Placeholder = "e.g., 203.0.113.1"
		// Try to auto-detect public IP
		if ip := detectPublicIP(); ip != "" {
			m.textInput.SetValue(ip)
		}
	case StepDomain:
		m.textInput.Placeholder = "e.g., node-1.orama.network"
	case StepPeerDomain:
		m.textInput.Placeholder = "e.g., node-123.orama.network"
	case StepClusterSecret:
		m.textInput.Placeholder = "64 hex characters"
		m.textInput.EchoMode = textinput.EchoPassword
	case StepSwarmKey:
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
	case StepPeerDomain:
		s.WriteString(m.viewPeerDomain())
	case StepClusterSecret:
		s.WriteString(m.viewClusterSecret())
	case StepSwarmKey:
		s.WriteString(m.viewSwarmKey())
	case StepBranch:
		s.WriteString(m.viewBranch())
	case StepNoPull:
		s.WriteString(m.viewNoPull())
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

func (m Model) viewPeerDomain() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Existing Node Domain") + "\n\n")
	s.WriteString("Enter the domain of an existing node to join:\n")
	s.WriteString(subtitleStyle.Render("The installer will auto-discover peer info via HTTPS/HTTP") + "\n\n")
	s.WriteString(m.textInput.View())

	if m.discovering {
		s.WriteString("\n\n" + subtitleStyle.Render("🔍 "+m.discoveryInfo))
	}

	if m.discoveredPeer != "" && m.err == nil {
		s.WriteString("\n\n" + successStyle.Render("✓ Discovered peer: "+m.discoveredPeer[:12]+"..."))
	}

	if m.err != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ " + m.err.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to discover & continue • Esc to go back"))
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

func (m Model) viewSwarmKey() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("IPFS Swarm Key") + "\n\n")
	s.WriteString("Enter the swarm key from an existing node:\n")
	s.WriteString(subtitleStyle.Render("Get it with: cat ~/.orama/secrets/swarm.key | tail -1") + "\n\n")
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

func (m Model) viewNoPull() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Git Repository") + "\n\n")
	s.WriteString("Pull latest changes from repository?\n\n")

	options := []string{"Pull latest (recommended)", "Skip git pull (use existing source)"}
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

	noPullStr := "Pull latest"
	if m.config.NoPull {
		noPullStr = "Skip git pull"
	}

	config := fmt.Sprintf(
		"  VPS IP:     %s\n"+
			"  Domain:     %s\n"+
			"  Branch:     %s\n"+
			"  Git Pull:   %s\n"+
			"  Node Type:  %s\n",
		m.config.VpsIP,
		m.config.Domain,
		m.config.Branch,
		noPullStr,
		map[bool]string{true: "First node (new cluster)", false: "Join existing cluster"}[m.config.IsFirstNode],
	)

	if !m.config.IsFirstNode {
		config += fmt.Sprintf("  Peer Node:  %s\n", m.config.PeerDomain)
		config += fmt.Sprintf("  Join Addr:  %s\n", m.config.JoinAddress)
		if len(m.config.Peers) > 0 {
			config += fmt.Sprintf("  Bootstrap:  %s...\n", m.config.Peers[0][:40])
		}
		if len(m.config.ClusterSecret) >= 8 {
			config += fmt.Sprintf("  Secret:     %s...\n", m.config.ClusterSecret[:8])
		}
		if len(m.config.SwarmKeyHex) >= 8 {
			config += fmt.Sprintf("  Swarm Key:  %s...\n", m.config.SwarmKeyHex[:8])
		}
		if m.config.IPFSPeerID != "" {
			config += fmt.Sprintf("  IPFS Peer:  %s...\n", m.config.IPFSPeerID[:16])
		}
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

// DiscoveryResult contains all information discovered from a peer node
type DiscoveryResult struct {
	PeerID         string   // LibP2P peer ID
	IPFSPeerID     string   // IPFS peer ID
	IPFSSwarmAddrs []string // IPFS swarm addresses
}

// discoverPeerFromDomain queries an existing node to get its peer ID and IPFS info
// Tries HTTPS first, then falls back to HTTP
// Respects DEBROS_TRUSTED_TLS_DOMAINS and DEBROS_CA_CERT_PATH environment variables for certificate verification
func discoverPeerFromDomain(domain string) (*DiscoveryResult, error) {
	// Use centralized TLS configuration that respects CA certificates and trusted domains
	client := tlsutil.NewHTTPClientForDomain(10*time.Second, domain)

	// Try HTTPS first
	url := fmt.Sprintf("https://%s/v1/network/status", domain)
	resp, err := client.Get(url)

	// If HTTPS fails, try HTTP
	if err != nil {
		// Finally try plain HTTP
		url = fmt.Sprintf("http://%s/v1/network/status", domain)
		resp, err = client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("could not connect to %s (tried HTTPS and HTTP): %w", domain, err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status from %s: %s", domain, resp.Status)
	}

	// Parse response including IPFS info
	var status struct {
		PeerID string `json:"peer_id"`
		NodeID string `json:"node_id"` // fallback for backward compatibility
		IPFS   *struct {
			PeerID         string   `json:"peer_id"`
			SwarmAddresses []string `json:"swarm_addresses"`
		} `json:"ipfs,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to parse response from %s: %w", domain, err)
	}

	// Use peer_id if available, otherwise fall back to node_id for backward compatibility
	peerID := status.PeerID
	if peerID == "" {
		peerID = status.NodeID
	}

	if peerID == "" {
		return nil, fmt.Errorf("no peer_id or node_id in response from %s", domain)
	}

	result := &DiscoveryResult{
		PeerID: peerID,
	}

	// Include IPFS info if available
	if status.IPFS != nil {
		result.IPFSPeerID = status.IPFS.PeerID
		result.IPFSSwarmAddrs = status.IPFS.SwarmAddresses
	}

	return result, nil
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

func validateSwarmKey(key string) error {
	if len(key) != 64 {
		return fmt.Errorf("swarm key must be 64 hex characters")
	}
	keyRegex := regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	if !keyRegex.MatchString(key) {
		return fmt.Errorf("swarm key must be valid hexadecimal")
	}
	return nil
}

// ensureCertificatesForDomain generates self-signed certificates for the domain
func ensureCertificatesForDomain(domain string) error {
	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create cert directory
	certDir := filepath.Join(home, ".orama", "certs")
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Create certificate manager
	cm := certutil.NewCertificateManager(certDir)

	// Ensure CA certificate exists
	caCertPEM, caKeyPEM, err := cm.EnsureCACertificate()
	if err != nil {
		return fmt.Errorf("failed to ensure CA certificate: %w", err)
	}

	// Ensure node certificate exists for the domain
	_, _, err = cm.EnsureNodeCertificate(domain, caCertPEM, caKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to ensure node certificate: %w", err)
	}

	// Also create wildcard certificate if domain is not already wildcard
	if !strings.HasPrefix(domain, "*.") {
		wildcardDomain := "*." + domain
		_, _, err = cm.EnsureNodeCertificate(wildcardDomain, caCertPEM, caKeyPEM)
		if err != nil {
			return fmt.Errorf("failed to ensure wildcard certificate: %w", err)
		}
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

// validateSNIDNSRecords checks if the required SNI DNS records exist
// It tries to resolve the key SNI hostnames for IPFS, IPFS Cluster, and Olric
// Note: Raft no longer uses SNI - it uses direct RQLite TLS on port 7002
// All should resolve to the same IP (the node's public IP or domain)
func validateSNIDNSRecords(domain string) error {
	// List of SNI services that need DNS records
	// Note: raft.domain is NOT included - RQLite uses direct TLS on port 7002
	sniServices := []string{
		fmt.Sprintf("ipfs.%s", domain),
		fmt.Sprintf("ipfs-cluster.%s", domain),
		fmt.Sprintf("olric.%s", domain),
	}

	// Try to resolve the main domain first to get baseline
	mainIPs, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("could not resolve main domain %s: %w", domain, err)
	}

	if len(mainIPs) == 0 {
		return fmt.Errorf("main domain %s resolved to no IP addresses", domain)
	}

	// Check each SNI service
	var unresolvedServices []string
	for _, service := range sniServices {
		ips, err := net.LookupHost(service)
		if err != nil || len(ips) == 0 {
			unresolvedServices = append(unresolvedServices, service)
		}
	}

	if len(unresolvedServices) > 0 {
		serviceList := strings.Join(unresolvedServices, ", ")
		return fmt.Errorf(
			"SNI DNS records not found for: %s\n\n"+
				"You need to add DNS records (A records or wildcard CNAME) for these services:\n"+
				"  - They should all resolve to the same IP as %s\n"+
				"  - Option 1: Add individual A records pointing to %s\n"+
				"  - Option 2: Add wildcard CNAME: *.%s -> %s\n\n"+
				"Without these records, multi-node clustering will fail.",
			serviceList, domain, domain, domain, domain,
		)
	}

	return nil
}

// Run starts the TUI installer and returns the configuration
func Run() (*InstallerConfig, error) {
	// Check if running as root
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("installer must be run as root (use sudo)")
	}

	model := NewModel()
	p := tea.NewProgram(&model, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	m := finalModel.(*Model)
	if m.step == StepInstalling || m.step == StepDone {
		config := m.GetConfig()
		return &config, nil
	}

	return nil, fmt.Errorf("installation cancelled")
}

