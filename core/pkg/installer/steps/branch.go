package steps

import (
	"strings"
)

// Branch step for selecting release channel
type Branch struct {
	Cursor int
}

// View renders the branch selection step
func (b *Branch) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Release Channel") + "\n\n")
	s.WriteString("Select the release channel:\n\n")

	options := []string{"main (stable)", "nightly (latest features)"}
	for i, opt := range options {
		if i == b.Cursor {
			s.WriteString(cursorStyle.Render("→ ") + focusedStyle.Render(opt) + "\n")
		} else {
			s.WriteString("  " + blurredStyle.Render(opt) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to select • Enter to confirm • Esc to go back"))
	return s.String()
}

// GetBranch returns the selected branch name
func (b *Branch) GetBranch() string {
	if b.Cursor == 0 {
		return "main"
	}
	return "nightly"
}
