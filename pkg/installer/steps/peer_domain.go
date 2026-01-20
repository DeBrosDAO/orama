package steps

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// PeerDomain step for entering existing node's domain to join
type PeerDomain struct {
	Input          textinput.Model
	Error          error
	Discovering    bool
	DiscoveryInfo  string
	DiscoveredPeer string
}

// NewPeerDomain creates a new PeerDomain step
func NewPeerDomain() *PeerDomain {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Placeholder = "e.g., node-123.orama.network"
	return &PeerDomain{
		Input: ti,
	}
}

// View renders the peer domain input step
func (p *PeerDomain) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Existing Node Domain") + "\n\n")
	s.WriteString("Enter the domain of an existing node to join:\n")
	s.WriteString(subtitleStyle.Render("The installer will auto-discover peer info via HTTPS/HTTP") + "\n\n")
	s.WriteString(p.Input.View())

	if p.Discovering {
		s.WriteString("\n\n" + subtitleStyle.Render("🔍 "+p.DiscoveryInfo))
	}

	if p.DiscoveredPeer != "" && p.Error == nil {
		s.WriteString("\n\n" + successStyle.Render("✓ Discovered peer: "+p.DiscoveredPeer[:12]+"..."))
	}

	if p.Error != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ "+p.Error.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to discover & continue • Esc to go back"))
	return s.String()
}

// SetValue sets the input value
func (p *PeerDomain) SetValue(value string) {
	p.Input.SetValue(value)
}

// Value returns the current input value
func (p *PeerDomain) Value() string {
	return strings.TrimSpace(p.Input.Value())
}

// SetError sets an error message
func (p *PeerDomain) SetError(err error) {
	p.Error = err
}

// SetDiscovering sets the discovery status
func (p *PeerDomain) SetDiscovering(discovering bool, info string) {
	p.Discovering = discovering
	p.DiscoveryInfo = info
}

// SetDiscoveredPeer sets the discovered peer ID
func (p *PeerDomain) SetDiscoveredPeer(peerID string) {
	p.DiscoveredPeer = peerID
}
