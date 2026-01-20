// Package installer provides an interactive TUI installer for Orama Network
package installer

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/installer/discovery"
	"github.com/DeBrosOfficial/network/pkg/installer/steps"
	"github.com/DeBrosOfficial/network/pkg/installer/validation"
)

// renderHeader renders the application header
func renderHeader() string {
	logo := `
   ___  ____      _    __  __    _
  / _ \|  _ \    / \  |  \/  |  / \
 | | | | |_) |  / _ \ | |\/| | / _ \
 | |_| |  _ <  / ___ \| |  | |/ ___ \
  \___/|_| \_\/_/   \_\_|  |_/_/   \_\
`
	return steps.TitleStyle.Render(logo) + "\n" + steps.SubtitleStyle.Render("Network Installation Wizard")
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
		if err := validation.ValidateIP(ip); err != nil {
			m.err = err
			return m, nil
		}
		m.config.VpsIP = ip
		m.err = nil
		m.step = StepDomain
		m.setupStepInput()

	case StepDomain:
		domain := strings.TrimSpace(m.textInput.Value())
		if err := validation.ValidateDomain(domain); err != nil {
			m.err = err
			return m, nil
		}

		// Check SNI DNS records for this domain (non-blocking warning)
		m.discovering = true
		m.discoveryInfo = "Checking SNI DNS records for " + domain + "..."

		if warning := validation.ValidateSNIDNSRecords(domain); warning != "" {
			// Log warning but continue - SNI DNS is optional for single-node setups
			m.sniWarning = warning
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
		if err := validation.ValidateDomain(peerDomain); err != nil {
			m.err = err
			return m, nil
		}

		// Check SNI DNS records for peer domain (non-blocking warning)
		m.discovering = true
		m.discoveryInfo = "Checking SNI DNS records for " + peerDomain + "..."

		if warning := validation.ValidateSNIDNSRecords(peerDomain); warning != "" {
			// Log warning but continue - peer might have different DNS setup
			m.sniWarning = warning
		}

		// Discover peer info from domain (try HTTPS first, then HTTP)
		m.discovering = true
		m.discoveryInfo = "Discovering peer from " + peerDomain + "..."

		disc, err := discovery.DiscoverPeerFromDomain(peerDomain)
		m.discovering = false

		if err != nil {
			m.err = fmt.Errorf("failed to discover peer: %w", err)
			return m, nil
		}

		// Store discovered info
		m.config.PeerDomain = peerDomain
		m.discoveredPeer = disc.PeerID

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
			fmt.Sprintf("/dns4/%s/tcp/4001/p2p/%s", peerDomain, disc.PeerID),
		}

		// Store IPFS peer info for Peering.Peers configuration
		if disc.IPFSPeerID != "" {
			m.config.IPFSPeerID = disc.IPFSPeerID
			m.config.IPFSSwarmAddrs = disc.IPFSSwarmAddrs
		}

		// Store IPFS Cluster peer info for cluster peer_addresses configuration
		if disc.IPFSClusterPeerID != "" {
			m.config.IPFSClusterPeerID = disc.IPFSClusterPeerID
			m.config.IPFSClusterAddrs = disc.IPFSClusterAddrs
		}

		m.err = nil
		m.step = StepClusterSecret
		m.setupStepInput()

	case StepClusterSecret:
		secret := strings.TrimSpace(m.textInput.Value())
		if err := validation.ValidateClusterSecret(secret); err != nil {
			m.err = err
			return m, nil
		}
		m.config.ClusterSecret = secret
		m.err = nil
		m.step = StepSwarmKey
		m.setupStepInput()

	case StepSwarmKey:
		swarmKey := strings.TrimSpace(m.textInput.Value())
		if err := config.ValidateSwarmKey(swarmKey); err != nil {
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
		if ip := validation.DetectPublicIP(); ip != "" {
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

// View renders the UI
func (m Model) View() string {
	var s strings.Builder

	// Header
	s.WriteString(renderHeader())
	s.WriteString("\n\n")

	switch m.step {
	case StepWelcome:
		welcome := &steps.Welcome{}
		s.WriteString(welcome.View())
	case StepNodeType:
		nodeType := &steps.NodeType{Cursor: m.cursor}
		s.WriteString(nodeType.View())
	case StepVpsIP:
		vpsIP := &steps.VpsIP{Input: m.textInput, Error: m.err}
		s.WriteString(vpsIP.View())
	case StepDomain:
		domain := &steps.Domain{Input: m.textInput, Error: m.err}
		s.WriteString(domain.View())
	case StepPeerDomain:
		peerDomain := &steps.PeerDomain{
			Input:          m.textInput,
			Error:          m.err,
			Discovering:    m.discovering,
			DiscoveryInfo:  m.discoveryInfo,
			DiscoveredPeer: m.discoveredPeer,
		}
		s.WriteString(peerDomain.View())
	case StepClusterSecret:
		clusterSecret := &steps.ClusterSecret{Input: m.textInput, Error: m.err}
		s.WriteString(clusterSecret.View())
	case StepSwarmKey:
		swarmKey := &steps.SwarmKey{Input: m.textInput, Error: m.err}
		s.WriteString(swarmKey.View())
	case StepBranch:
		branch := &steps.Branch{Cursor: m.cursor}
		s.WriteString(branch.View())
	case StepNoPull:
		noPull := &steps.NoPull{Cursor: m.cursor}
		s.WriteString(noPull.View())
	case StepConfirm:
		confirm := &steps.Confirm{
			VpsIP:         m.config.VpsIP,
			Domain:        m.config.Domain,
			Branch:        m.config.Branch,
			NoPull:        m.config.NoPull,
			IsFirstNode:   m.config.IsFirstNode,
			PeerDomain:    m.config.PeerDomain,
			JoinAddress:   m.config.JoinAddress,
			Peers:         m.config.Peers,
			ClusterSecret: m.config.ClusterSecret,
			SwarmKeyHex:   m.config.SwarmKeyHex,
			IPFSPeerID:    m.config.IPFSPeerID,
			SNIWarning:    m.sniWarning,
		}
		s.WriteString(confirm.View())
	case StepInstalling:
		installing := &steps.Installing{Output: m.installOutput}
		s.WriteString(installing.View())
	case StepDone:
		done := &steps.Done{}
		s.WriteString(done.View())
	}

	return s.String()
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
