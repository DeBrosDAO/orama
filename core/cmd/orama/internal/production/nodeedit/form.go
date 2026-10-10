package nodeedit

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

const (
	// storageDigits bounds the capacity field: a GB count of at most 9 digits.
	storageDigits = 9
	// chainIDLength bounds the node id field.
	chainIDLength = 64
	// cursorGlyph marks where typing goes in the focused text field.
	cursorGlyph = "_"
)

// textField is a one-line text input: typed runes append, backspace deletes.
type textField struct {
	value       string
	limit       int
	placeholder string
	focused     bool
}

// handle applies a key to the field and reports whether it was an edit.
func (t *textField) handle(key tea.KeyMsg) {
	switch key.Type {
	case tea.KeyBackspace:
		if r := []rune(t.value); len(r) > 0 {
			t.value = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		if len([]rune(t.value))+len(key.Runes) <= t.limit {
			t.value += string(key.Runes)
		}
	}
}

func (t textField) view() string {
	switch {
	case t.value == "" && !t.focused:
		return "[" + t.placeholder + "]"
	case t.focused:
		return "[" + t.value + cursorGlyph + "]"
	default:
		return "[" + t.value + "]"
	}
}

// field is one input of the edit form.
type field int

const (
	fieldStorage field = iota
	fieldChainID
	fieldExit
)

// editModel is the form for one node's settings.
type editModel struct {
	host    string
	st      NodeState
	fields  []field
	focus   int
	storage textField
	chain   textField
	exit    bool

	problem   string
	submitted bool
	cancelled bool
}

func newEditModel(host string, st NodeState) editModel {
	m := editModel{host: host, st: st, exit: st.Exit}
	m.storage = textField{limit: storageDigits, placeholder: "GB"}
	m.chain = textField{limit: chainIDLength, placeholder: "the node's id on the chain"}
	if st.IPFS {
		m.fields = append(m.fields, fieldStorage, fieldChainID)
	}
	if st.Relay {
		m.fields = append(m.fields, fieldExit)
	}
	m.refocus()
	return m
}

func (m editModel) Init() tea.Cmd { return nil }

// current is the field that has the focus; ok is false when the node has nothing to edit.
func (m editModel) current() (field, bool) {
	if len(m.fields) == 0 {
		return 0, false
	}
	return m.fields[m.focus], true
}

// refocus gives the text cursor to the focused field only.
func (m *editModel) refocus() {
	f, _ := m.current()
	m.storage.focused = f == fieldStorage
	m.chain.focused = f == fieldChainID
}

func (m editModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "esc", "ctrl+c":
		m.cancelled = true
		return m, tea.Quit
	case "tab", "down":
		return m.moveFocus(1), nil
	case "shift+tab", "up":
		return m.moveFocus(-1), nil
	case "enter":
		return m.submit()
	}
	f, has := m.current()
	if !has {
		return m, nil
	}
	m.problem = ""
	switch f {
	case fieldExit:
		if key.String() == " " {
			m.exit = !m.exit
		}
		return m, nil
	case fieldStorage:
		m.storage.handle(key)
	default:
		m.chain.handle(key)
	}
	return m, nil
}

func (m editModel) moveFocus(delta int) editModel {
	if len(m.fields) > 0 {
		m.focus = (m.focus + delta + len(m.fields)) % len(m.fields)
		m.refocus()
	}
	return m
}

// submit validates the form and ends it.
func (m editModel) submit() (tea.Model, tea.Cmd) {
	if _, _, err := m.result(); err != nil {
		m.problem = err.Error()
		return m, nil
	}
	m.submitted = true
	return m, tea.Quit
}

// result is the settings the form holds and the chain node id.
func (m editModel) result() (Settings, string, error) {
	var s Settings
	if text := strings.TrimSpace(m.storage.value); text != "" {
		gb, err := strconv.ParseUint(text, 10, 64)
		if err != nil || gb == 0 {
			return Settings{}, "", fmt.Errorf("the capacity %q is not a whole number of GB above zero", text)
		}
		s.StorageGB = &gb
	}
	if m.exit != m.st.Exit {
		exit := m.exit
		s.Exit = &exit
	}
	chainID := strings.TrimSpace(m.chain.value)
	switch {
	case s.Empty():
		return Settings{}, "", fmt.Errorf("nothing is changed; esc leaves the form")
	case s.StorageGB != nil && chainID == "":
		return Settings{}, "", fmt.Errorf("a new capacity is declared on the chain: enter the node's id there")
	}
	return s, chainID, nil
}

func (m editModel) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Edit %s\n\n", m.host)
	if len(m.fields) == 0 {
		b.WriteString("This node has no public storage and no Tor relay: there is nothing to change here.\n")
	}
	if m.st.IPFS {
		fmt.Fprintf(&b, "%s Storage capacity (GB; Kubo StorageMax is %s now)  %s\n", m.marker(fieldStorage), m.st.StorageMax, m.storage.view())
		fmt.Fprintf(&b, "%s Node id on the chain (for the capacity)           %s\n", m.marker(fieldChainID), m.chain.view())
	}
	if m.st.Relay {
		fmt.Fprintf(&b, "%s Exit relay  %s  (space toggles)\n", m.marker(fieldExit), checkbox(m.exit))
	}
	fmt.Fprintf(&b, "\n  Global layer: %s. It cannot be turned on or off here: running setup again for this IP adds it, `orama remove` takes the node out.\n", onOff(m.st.Global))
	if m.problem != "" {
		fmt.Fprintf(&b, "\n  ! %s\n", m.problem)
	}
	b.WriteString("\ntab moves, enter applies, esc cancels\n")
	return b.String()
}

func (m editModel) marker(f field) string {
	if cur, ok := m.current(); ok && cur == f {
		return ">"
	}
	return " "
}

func checkbox(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}

func onOff(on bool) string {
	if on {
		return "installed"
	}
	return "not installed"
}

// editForm asks for the settings of one node.
func editForm(host string, st NodeState) (Settings, string, error) {
	final, err := tea.NewProgram(newEditModel(host, st)).Run()
	if err != nil {
		return Settings{}, "", clierr.Failure("the edit form: %v", err)
	}
	m := final.(editModel)
	if !m.submitted {
		return Settings{}, "", clierr.Aborted("nothing was changed")
	}
	return m.result()
}
