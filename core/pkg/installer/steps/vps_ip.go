package steps

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// VpsIP step for entering server IP address
type VpsIP struct {
	Input textinput.Model
	Error error
}

// NewVpsIP creates a new VpsIP step
func NewVpsIP() *VpsIP {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Placeholder = "e.g., 203.0.113.1"
	return &VpsIP{
		Input: ti,
	}
}

// View renders the VPS IP input step
func (v *VpsIP) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Server IP Address") + "\n\n")
	s.WriteString("Enter your server's public IP address:\n\n")
	s.WriteString(v.Input.View())

	if v.Error != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ "+v.Error.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

// SetValue sets the input value
func (v *VpsIP) SetValue(value string) {
	v.Input.SetValue(value)
}

// Value returns the current input value
func (v *VpsIP) Value() string {
	return strings.TrimSpace(v.Input.Value())
}

// SetError sets an error message
func (v *VpsIP) SetError(err error) {
	v.Error = err
}
