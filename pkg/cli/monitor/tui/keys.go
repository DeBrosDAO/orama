package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Quit       key.Binding
	NextTab    key.Binding
	PrevTab    key.Binding
	Refresh    key.Binding
	ScrollUp   key.Binding
	ScrollDown key.Binding
}

var keys = keyMap{
	Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	NextTab:    key.NewBinding(key.WithKeys("tab", "l"), key.WithHelp("tab", "next tab")),
	PrevTab:    key.NewBinding(key.WithKeys("shift+tab", "h"), key.WithHelp("shift+tab", "prev tab")),
	Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	ScrollUp:   key.NewBinding(key.WithKeys("up", "k")),
	ScrollDown: key.NewBinding(key.WithKeys("down", "j")),
}
