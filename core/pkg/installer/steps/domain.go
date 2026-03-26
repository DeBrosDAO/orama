package steps

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// Domain step for entering domain name
type Domain struct {
	Input textinput.Model
	Error error
}

// NewDomain creates a new Domain step
func NewDomain() *Domain {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Placeholder = "e.g., node-1.orama.network"
	return &Domain{
		Input: ti,
	}
}

// View renders the domain input step
func (d *Domain) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Domain Name") + "\n\n")
	s.WriteString("Enter the domain for this node:\n\n")
	s.WriteString(d.Input.View())

	if d.Error != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ "+d.Error.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

// SetValue sets the input value
func (d *Domain) SetValue(value string) {
	d.Input.SetValue(value)
}

// Value returns the current input value
func (d *Domain) Value() string {
	return strings.TrimSpace(d.Input.Value())
}

// SetError sets an error message
func (d *Domain) SetError(err error) {
	d.Error = err
}
