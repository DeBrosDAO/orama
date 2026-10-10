package wizard

import (
	"errors"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

// startHostKeys scans the first machine; the keys are asked about one by one.
func (m *Model) startHostKeys() (tea.Model, tea.Cmd) {
	m.hostIPs, m.hostKeyIdx, m.hostKeys = m.opts.IPs, 0, nil
	m.opts.HostKeys = map[string]string{}
	return m, m.scan(m.hostIPs[0])
}

func (m *Model) onHostKeys(msg hostKeysMsg) (tea.Model, tea.Cmd) {
	// An answer for a machine that is no longer the one asked about (the person
	// went back and forward) must not be shown under another's address.
	if m.step != stepHostKeys || msg.ip != m.currentHost() {
		return m, nil
	}
	if msg.err != nil || len(msg.keys) == 0 {
		return m.fail("could not read the SSH host key of %s: %v", msg.ip, orErr(msg.err, errors.New("it presented none")))
	}
	m.hostKeys = msg.keys
	return m, nil
}

// onHostKeyKey: the number of the fingerprint the provider's console shows
// trusts it; n refuses the machine.
func (m *Model) onHostKeyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.hostKeys) == 0 {
		return m, nil
	}
	if msg.String() == "n" {
		m.hostKeys = nil
		return m.fail("host key of %s not confirmed; check the fingerprint in your provider's console, or press esc to change the addresses", m.hostIPs[m.hostKeyIdx])
	}
	n, err := strconv.Atoi(msg.String())
	if err != nil || n < 1 || n > len(m.hostKeys) {
		return m, nil
	}
	m.opts.HostKeys[m.hostIPs[m.hostKeyIdx]] = m.hostKeys[n-1].Fingerprint
	m.hostKeyIdx++
	m.hostKeys = nil
	if m.hostKeyIdx < len(m.hostIPs) {
		return m, m.scan(m.hostIPs[m.hostKeyIdx])
	}
	return m.goTo(stepNetwork)
}

// onOptionsKey: space toggles a row, enter goes on. Choosing the exit role asks
// for the warning to be accepted first.
func (m *Model) onOptionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.exitAsked {
		return m.answerExit(msg)
	}
	switch msg.Type {
	case tea.KeyUp:
		m.cursor = (m.cursor + optCount - 1) % optCount
	case tea.KeyDown, tea.KeyTab:
		m.cursor = (m.cursor + 1) % optCount
	case tea.KeySpace:
		return m.toggle(m.cursor)
	case tea.KeyEnter:
		return m.takeOptions()
	}
	return m, nil
}

func (m *Model) toggle(row int) (tea.Model, tea.Cmd) {
	switch row {
	case optGlobal:
		m.toggles[optGlobal] = !m.toggles[optGlobal]
		if !m.toggles[optGlobal] {
			m.toggles[optRelay], m.toggles[optExit] = false, false
		}
	case optRelay:
		m.toggles[optRelay] = m.toggles[optGlobal] && !m.toggles[optRelay]
		if !m.toggles[optRelay] {
			m.toggles[optExit] = false
		}
	case optExit:
		if m.toggles[optExit] {
			m.toggles[optExit] = false
		} else if m.toggles[optRelay] {
			m.exitAsked = true
		}
	}
	return m, nil
}

func (m *Model) answerExit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.toggles[optExit], m.opts.ExitConfirmed = true, true
		m.exitAsked = false
	case "n":
		m.exitAsked = false
	}
	return m, nil
}

func (m *Model) takeOptions() (tea.Model, tea.Cmd) {
	m.opts.ClusterOnly = !m.toggles[optGlobal]
	m.opts.Exit = m.toggles[optExit]
	if !m.toggles[optRelay] {
		m.opts.TorNetwork = ""
	}
	// Leaving the relay out of a network that pins its Tor network file has to be said: the plan
	// would otherwise give the nodes the relay the pin makes the default.
	m.opts.NoRelay = !m.opts.ClusterOnly && !m.toggles[optRelay] && m.torPinned
	if m.opts.ClusterOnly {
		m.opts.StorageGB = 0
		return m.goTo(stepName)
	}
	return m.goTo(stepStorage)
}

func (m *Model) afterStorage() (tea.Model, tea.Cmd) {
	if m.toggles[optRelay] && !m.torPinned {
		return m.goTo(stepTor)
	}
	return m.goTo(stepName)
}
