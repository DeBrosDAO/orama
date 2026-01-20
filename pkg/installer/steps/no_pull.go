package steps

import (
	"strings"
)

// NoPull step for selecting whether to pull latest changes
type NoPull struct {
	Cursor int
}

// View renders the no-pull selection step
func (n *NoPull) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Git Repository") + "\n\n")
	s.WriteString("Pull latest changes from repository?\n\n")

	options := []string{"Pull latest (recommended)", "Skip git pull (use existing source)"}
	for i, opt := range options {
		if i == n.Cursor {
			s.WriteString(cursorStyle.Render("→ ") + focusedStyle.Render(opt) + "\n")
		} else {
			s.WriteString("  " + blurredStyle.Render(opt) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to select • Enter to confirm • Esc to go back"))
	return s.String()
}

// ShouldPull returns true if should pull latest changes
func (n *NoPull) ShouldPull() bool {
	return n.Cursor == 0
}
