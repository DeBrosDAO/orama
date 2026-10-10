package nodeedit

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// selectModel picks the node to edit.
type selectModel struct {
	hosts     []string
	cursor    int
	chosen    string
	cancelled bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.hosts)-1 {
			m.cursor++
		}
	case "enter":
		m.chosen = m.hosts[m.cursor]
		return m, tea.Quit
	case "esc", "ctrl+c", "q":
		m.cancelled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m selectModel) View() string {
	var b strings.Builder
	b.WriteString("Which node do you want to edit?\n\n")
	for i, h := range m.hosts {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		b.WriteString(cursor + h + "\n")
	}
	b.WriteString("\n↑/↓ move, enter chooses, esc cancels\n")
	return b.String()
}

// chooseNodeForm asks which of hosts to edit.
func chooseNodeForm(hosts []string) (string, error) {
	if len(hosts) == 0 {
		return "", clierr.NotFound("this network has no node to edit")
	}
	final, err := tea.NewProgram(selectModel{hosts: hosts}).Run()
	if err != nil {
		return "", clierr.Failure("the node form: %v", err)
	}
	m := final.(selectModel)
	if m.cancelled {
		return "", clierr.Aborted("no node chosen")
	}
	return m.chosen, nil
}
