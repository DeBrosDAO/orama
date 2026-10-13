package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is every key the live view answers to. Handlers match with
// key.Matches, and the help overlay is rendered from the same bindings, so
// the help cannot list a key that does nothing.
type keyMap struct {
	Quit     key.Binding
	NextTab  key.Binding
	PrevTab  key.Binding
	JumpTab  key.Binding
	Up       key.Binding
	Down     key.Binding
	Enter    key.Binding
	Back     key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Critical key.Binding
	Warning  key.Binding
	Info     key.Binding
	All      key.Binding
}

var keys = keyMap{
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	NextTab:  key.NewBinding(key.WithKeys("tab", "l"), key.WithHelp("tab", "next tab")),
	PrevTab:  key.NewBinding(key.WithKeys("shift+tab", "h"), key.WithHelp("shift+tab", "previous tab")),
	JumpTab:  key.NewBinding(key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9", "0"), key.WithHelp("1-9,0", "jump to tab")),
	Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up / select")),
	Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down / select")),
	Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open node detail (Nodes)")),
	Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back / close help")),
	Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh now")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "toggle help")),
	Critical: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "critical alerts (Alerts)")),
	Warning:  key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "warnings (Alerts)")),
	Info:     key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "info alerts (Alerts)")),
	All:      key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all alerts (Alerts)")),
}

// helpBindings is the order the help overlay lists the keys in.
func (k keyMap) helpBindings() []key.Binding {
	return []key.Binding{k.NextTab, k.PrevTab, k.JumpTab, k.Up, k.Down, k.Enter, k.Back,
		k.Critical, k.Warning, k.Info, k.All, k.Refresh, k.Help, k.Quit}
}
