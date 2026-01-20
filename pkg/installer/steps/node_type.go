package steps

import (
	"strings"
)

// NodeType step for selecting whether this is first node or joining existing cluster
type NodeType struct {
	Cursor int
}

// View renders the node type selection step
func (nt *NodeType) View() string {
	var s strings.Builder
	s.WriteString(titleStyle.Render("Node Type") + "\n\n")
	s.WriteString("Is this the first node in a new cluster?\n\n")

	options := []string{"Yes, create new cluster", "No, join existing cluster"}
	for i, opt := range options {
		if i == nt.Cursor {
			s.WriteString(cursorStyle.Render("→ ") + focusedStyle.Render(opt) + "\n")
		} else {
			s.WriteString("  " + blurredStyle.Render(opt) + "\n")
		}
	}

	s.WriteString("\n")
	s.WriteString(helpStyle.Render("↑/↓ to select • Enter to confirm • Esc to go back"))
	return s.String()
}

// IsFirstNode returns true if creating new cluster is selected
func (nt *NodeType) IsFirstNode() bool {
	return nt.Cursor == 0
}
