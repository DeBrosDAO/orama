package nodeedit

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m tea.Model, keys ...tea.KeyMsg) tea.Model {
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m
}

func typed(s string) []tea.KeyMsg {
	var out []tea.KeyMsg
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

var (
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyTab   = tea.KeyMsg{Type: tea.KeyTab}
	keySpace = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	keyBack  = tea.KeyMsg{Type: tea.KeyBackspace}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	keyDown  = tea.KeyMsg{Type: tea.KeyDown}
)

func TestSelectModel_choosesTheNodeUnderTheCursor(t *testing.T) {
	var m tea.Model = selectModel{hosts: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}}

	m = press(m, keyDown, keyDown, keyDown, tea.KeyMsg{Type: tea.KeyUp}, keyEnter)

	if got := m.(selectModel).chosen; got != "10.0.0.2" {
		t.Errorf("chosen = %q, want the second node (the cursor stops at the ends)", got)
	}
}

func TestSelectModel_escChoosesNothing(t *testing.T) {
	m := press(selectModel{hosts: []string{"10.0.0.1"}}, keyEsc).(selectModel)

	if !m.cancelled || m.chosen != "" {
		t.Errorf("model = %+v", m)
	}
}

func TestSelectModel_viewListsTheNodesAndMarksTheCursor(t *testing.T) {
	v := selectModel{hosts: []string{"10.0.0.1", "10.0.0.2"}, cursor: 1}.View()

	if !strings.Contains(v, "> 10.0.0.2") || !strings.Contains(v, "  10.0.0.1") {
		t.Errorf("view:\n%s", v)
	}
}

func TestEditModel_storageAndChainIDSubmit(t *testing.T) {
	var m tea.Model = newEditModel("10.0.0.1", fullNode)

	m = press(m, typed("100")...)
	m = press(m, keyTab)
	m = press(m, typed("node-1")...)
	m = press(m, keyEnter)

	em := m.(editModel)
	if !em.submitted {
		t.Fatalf("not submitted: %q", em.problem)
	}
	s, chainID, err := em.result()
	if err != nil || s.StorageGB == nil || *s.StorageGB != 100 || chainID != "node-1" || s.Exit != nil {
		t.Errorf("result = %+v, %q, %v", s, chainID, err)
	}
}

func TestEditModel_backspaceEditsTheField(t *testing.T) {
	m := press(newEditModel("h", fullNode), typed("1005")...)
	m = press(m, keyBack)

	if got := m.(editModel).storage.value; got != "100" {
		t.Errorf("storage = %q", got)
	}
}

func TestEditModel_aCapacityNeedsTheChainID(t *testing.T) {
	m := press(newEditModel("h", fullNode), typed("100")...)
	m = press(m, keyEnter)

	em := m.(editModel)
	if em.submitted || !strings.Contains(em.problem, "enter the node's id") {
		t.Errorf("submitted %v, problem %q", em.submitted, em.problem)
	}
}

func TestEditModel_aBadCapacityIsReportedAndKeepsTheForm(t *testing.T) {
	m := press(newEditModel("h", fullNode), typed("12x")...)
	m = press(m, keyEnter)

	em := m.(editModel)
	if em.submitted || !strings.Contains(em.problem, `"12x"`) {
		t.Errorf("submitted %v, problem %q", em.submitted, em.problem)
	}
}

func TestEditModel_spaceTogglesTheExitRoleAndOnlyAChangeCounts(t *testing.T) {
	st := NodeState{Global: true, Relay: true}
	m := press(newEditModel("h", st), keySpace, keyEnter).(editModel)

	s, _, err := m.result()
	if !m.submitted || err != nil || s.Exit == nil || !*s.Exit {
		t.Fatalf("submitted %v, result %+v, %v", m.submitted, s, err)
	}

	again := press(newEditModel("h", st), keySpace, keySpace, keyEnter).(editModel)
	if again.submitted || !strings.Contains(again.problem, "nothing is changed") {
		t.Errorf("toggling twice changes nothing: submitted %v, problem %q", again.submitted, again.problem)
	}
}

func TestEditModel_aNodeWithNothingToEditCannotSubmit(t *testing.T) {
	m := press(newEditModel("h", NodeState{}), keyTab, keyEnter).(editModel)

	if m.submitted {
		t.Error("a node with no storage and no relay submitted a form")
	}
	if v := m.View(); !strings.Contains(v, "nothing to change here") || !strings.Contains(v, "not installed") {
		t.Errorf("view:\n%s", v)
	}
}

func TestEditModel_escCancels(t *testing.T) {
	m := press(newEditModel("h", fullNode), typed("1")...)
	m = press(m, keyEsc)

	if em := m.(editModel); !em.cancelled || em.submitted {
		t.Errorf("model = %+v", em)
	}
}

func TestEditModel_viewShowsWhatItEditsAndWhatItDoesNot(t *testing.T) {
	v := newEditModel("10.0.0.7", fullNode).View()

	for _, want := range []string{"Edit 10.0.0.7", "StorageMax is 55GB", "Exit relay", "[ ]", "Global layer: installed", "running setup again", "orama remove"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestTextField_limitsItsLength(t *testing.T) {
	f := textField{limit: 3}
	for _, k := range typed("12345") {
		f.handle(k)
	}
	if f.value != "123" {
		t.Errorf("value = %q, want the limit kept", f.value)
	}
}
