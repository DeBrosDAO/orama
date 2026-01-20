package steps

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// ClusterSecret step for entering cluster secret
type ClusterSecret struct {
	Input textinput.Model
	Error error
}

// NewClusterSecret creates a new ClusterSecret step
func NewClusterSecret() *ClusterSecret {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50
	ti.Placeholder = "64 hex characters"
	ti.EchoMode = textinput.EchoPassword
	return &ClusterSecret{
		Input: ti,
	}
}

// View renders the cluster secret input step
func (c *ClusterSecret) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Cluster Secret") + "\n\n")
	s.WriteString("Enter the cluster secret from an existing node:\n")
	s.WriteString(subtitleStyle.Render("Get it with: cat ~/.orama/secrets/cluster-secret") + "\n\n")
	s.WriteString(c.Input.View())

	if c.Error != nil {
		s.WriteString("\n\n" + errorStyle.Render("✗ "+c.Error.Error()))
	}

	s.WriteString("\n\n")
	s.WriteString(helpStyle.Render("Enter to confirm • Esc to go back"))
	return s.String()
}

// SetValue sets the input value
func (c *ClusterSecret) SetValue(value string) {
	c.Input.SetValue(value)
}

// Value returns the current input value
func (c *ClusterSecret) Value() string {
	return strings.TrimSpace(c.Input.Value())
}

// SetError sets an error message
func (c *ClusterSecret) SetError(err error) {
	c.Error = err
}
