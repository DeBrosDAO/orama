package steps

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// SwarmKey step for entering IPFS swarm key
type SwarmKey struct {
	Input textinput.Model
	Error error
}

// NewSwarmKey creates a new SwarmKey step
func NewSwarmKey() *SwarmKey {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Placeholder = "64 hex characters"
	ti.EchoMode = textinput.EchoPassword
	return &SwarmKey{
		Input: ti,
	}
}

// View renders the swarm key input step
func (s *SwarmKey) View() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("IPFS Swarm Key") + "\n\n")
	sb.WriteString("Enter the swarm key from an existing node:\n")
	sb.WriteString(subtitleStyle.Render("Get it with: cat ~/.orama/secrets/swarm.key | tail -1") + "\n\n")
	sb.WriteString(s.Input.View())

	if s.Error != nil {
		sb.WriteString("\n\n" + errorStyle.Render("✗ "+s.Error.Error()))
	}

	sb.WriteString("\n\n")
	sb.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return sb.String()
}

// SetValue sets the input value
func (s *SwarmKey) SetValue(value string) {
	s.Input.SetValue(value)
}

// Value returns the current input value
func (s *SwarmKey) Value() string {
	return strings.TrimSpace(s.Input.Value())
}

// SetError sets an error message
func (s *SwarmKey) SetError(err error) {
	s.Error = err
}
