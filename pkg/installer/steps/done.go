package steps

import (
	"strings"
)

// Done step shown after successful installation
type Done struct{}

// View renders the done step
func (d *Done) View() string {
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
