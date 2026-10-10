package wizard

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// sshUserRE is a POSIX login name.
var sshUserRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func orDefaultNum(v, fallback uint64) string {
	if v == 0 {
		v = fallback
	}
	return strconv.FormatUint(v, 10)
}

func (m *Model) fail(format string, args ...any) (tea.Model, tea.Cmd) {
	m.err = fmt.Sprintf(format, args...)
	return m, nil
}

func (m *Model) onWallet(msg walletMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.walletErr = msg.err
		m.err = msg.err.Error()
		return m, nil
	}
	// The first question is the start of the history: there is no going back to
	// the wallet check.
	return m.enter(stepIPs)
}

// onInputKey: enter takes the text of a question; any other key edits it.
func (m *Model) onInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyEnter {
		m.input.Update(msg)
		return m, nil
	}
	value := strings.TrimSpace(m.input.Value())
	switch m.step {
	case stepIPs:
		return m.takeIPs(value)
	case stepUser:
		if !sshUserRE.MatchString(value) {
			return m.fail("%q is not a login name", value)
		}
		m.opts.User = value
		return m.goTo(stepLogin)
	case stepSecret:
		return m.takeSecret(value)
	case stepName:
		return m.takeName(value)
	case stepStorage:
		gb, err := strconv.ParseUint(value, 10, 64)
		if err != nil || gb == 0 {
			return m.fail("%q is not a number of gigabytes", value)
		}
		m.opts.StorageGB = gb
		return m.afterStorage()
	case stepTor:
		if value == "" {
			return m.fail("give the path of the Tor network file, or go back and leave the relay out")
		}
		m.opts.TorNetwork = value
		return m.goTo(stepName)
	}
	return m, nil
}

func (m *Model) takeIPs(value string) (tea.Model, tea.Cmd) {
	ips, err := setup.ParseIPList(value)
	if err != nil {
		return m.fail("%v", err)
	}
	m.opts.IPs, m.opts.HostKeys = ips, nil
	return m.goTo(stepUser)
}

func (m *Model) takeSecret(value string) (tea.Model, tea.Cmd) {
	if value == "" && m.login == loginKeyFile {
		return m.fail("give the path of the private key")
	}
	if m.login == loginTypedPassword {
		m.opts.Password = m.input.Value()
		if m.opts.Password == "" {
			return m.fail("type the password")
		}
	} else {
		m.opts.BootstrapKey = value
	}
	return m.goTo(stepHostKeys)
}

func (m *Model) takeName(value string) (tea.Model, tea.Cmd) {
	if value == "" && m.opts.ClusterOnly {
		m.opts.Name = ""
		return m.goTo(stepInspect)
	}
	if err := setup.ValidateNodeName(strings.ToLower(value)); err != nil {
		return m.fail("%v", err)
	}
	m.opts.Name = strings.ToLower(value)
	return m.goTo(stepInspect)
}

// onListKey moves in the login and network lists and takes a choice.
func (m *Model) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	size := len(loginLabels)
	if m.step == stepNetwork {
		size = len(m.networks)
	}
	switch msg.Type {
	case tea.KeyUp:
		m.cursor = (m.cursor + size - 1) % size
	case tea.KeyDown, tea.KeyTab:
		m.cursor = (m.cursor + 1) % size
	case tea.KeyEnter:
		if m.step == stepLogin {
			return m.takeLogin(loginMethod(m.cursor))
		}
		return m.takeNetwork(m.networks[m.cursor])
	}
	return m, nil
}

func (m *Model) takeLogin(method loginMethod) (tea.Model, tea.Cmd) {
	m.login = method
	m.opts.UsePassword = method == loginVaultPassword || method == loginTypedPassword
	m.opts.Password, m.opts.BootstrapKey = "", ""
	if method == loginTypedPassword || method == loginKeyFile {
		return m.goTo(stepSecret)
	}
	return m.goTo(stepHostKeys)
}

func (m *Model) takeNetwork(n NetworkChoice) (tea.Model, tea.Cmd) {
	m.opts.Network = n.Name
	return m.goTo(stepOptions)
}

func (m *Model) onNetworks(msg networksMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || len(msg.choices) == 0 {
		return m.fail("%v", orErr(msg.err, errors.New("this CLI knows no network: add one with `orama network add <manifest-url>`")))
	}
	m.networks = msg.choices
	for i, c := range msg.choices {
		if c.Default {
			m.cursor = i
		}
	}
	if len(msg.choices) == 1 {
		return m.takeNetwork(msg.choices[0])
	}
	return m, nil
}

func orErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// startHostKeys scans the first machine; the keys are asked about one by one.
func (m *Model) startHostKeys() (tea.Model, tea.Cmd) {
	m.hostIPs, m.hostKeyIdx, m.hostKeys = m.opts.IPs, 0, nil
	m.opts.HostKeys = map[string]string{}
	return m, m.scan(m.hostIPs[0])
}

func (m *Model) onHostKeys(msg hostKeysMsg) (tea.Model, tea.Cmd) {
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
	if m.opts.ClusterOnly {
		m.opts.StorageGB = 0
		return m.goTo(stepName)
	}
	return m.goTo(stepStorage)
}

func (m *Model) afterStorage() (tea.Model, tea.Cmd) {
	if m.toggles[optRelay] {
		return m.goTo(stepTor)
	}
	return m.goTo(stepName)
}

func (m *Model) onInspect(msg inspectMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.fail("%v", msg.err)
	}
	m.inspections = msg.results
	return m, nil
}

func (m *Model) inspectOK() bool {
	if len(m.inspections) == 0 {
		return false
	}
	for _, i := range m.inspections {
		if i.Err != nil {
			return false
		}
	}
	return true
}

func (m *Model) onPlan(msg planMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err.Error()
		return m, nil
	}
	m.plan = msg.plan
	return m, nil
}

// onConfirmKey: enter or y starts the run, n or esc goes back.
func (m *Model) onConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.plan == nil {
		return m, nil
	}
	switch msg.String() {
	case "y", "enter":
		return m.goTo(stepRun)
	case "n":
		return m.back()
	}
	return m, nil
}

// onInspectKey: enter goes on when every machine is enough; esc goes back to
// change the answers; q leaves.
func (m *Model) onInspectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "q":
		return m.quit(errors.New("stopped before anything was changed"))
	case msg.Type == tea.KeyEsc:
		return m.back()
	case msg.Type == tea.KeyEnter && m.inspectOK():
		return m.goTo(stepConfirm)
	}
	return m, nil
}
