package steps

import (
	"strings"
)

// Installing step shown during installation
type Installing struct {
	Output []string
}

// View renders the installing step
func (i *Installing) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Installing...") + "\n\n")
	s.WriteString("Please wait while the node is being configured.\n\n")
	for _, line := range i.Output {
		s.WriteString(line + "\n")
	}
	return s.String()
}
