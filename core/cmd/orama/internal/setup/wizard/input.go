package wizard

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// input is a one-line text field: typed characters go on the end, backspace
// takes one off, and a password shows a bullet for each. It has no cursor
// movement, because every answer here is short and retyping it is quicker.
type input struct {
	value       []rune
	Placeholder string
	Password    bool
}

func (i *input) SetValue(v string) { i.value = []rune(v) }
func (i *input) Value() string     { return string(i.value) }

// Update applies a key to the field.
func (i *input) Update(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		i.value = append(i.value, msg.Runes...)
	case tea.KeyBackspace:
		if len(i.value) > 0 {
			i.value = i.value[:len(i.value)-1]
		}
	case tea.KeyCtrlU:
		i.value = nil
	}
}

// View draws the field with its prompt.
func (i *input) View() string {
	switch {
	case len(i.value) == 0:
		return "> " + faint(i.Placeholder)
	case i.Password:
		return "> " + strings.Repeat("*", len(i.value))
	}
	return "> " + string(i.value)
}
