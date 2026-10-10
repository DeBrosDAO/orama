package wizard

import (
	"context"
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

type step int

const (
	stepWallet step = iota
	stepIPs
	stepUser
	stepLogin
	stepSecret
	stepHostKeys
	stepNetwork
	stepName
	stepOptions
	stepStorage
	stepTor
	stepInspect
	stepConfirm
	stepRun
	stepDone
)

// loginMethod is how the first connection to a machine is made.
type loginMethod int

const (
	loginKeyInstalled loginMethod = iota
	loginVaultPassword
	loginTypedPassword
	loginKeyFile
)

var loginLabels = []string{
	"My RootWallet already has an SSH key on these machines",
	"Use the password saved in my RootWallet vault (rw vault add <ip>)",
	"Type the machines' password now (used once, never saved)",
	"Use a private key file that opens them today",
}

// option rows of the options step.
const (
	optGlobal = iota
	optRelay
	optExit
	optCount
)

// Messages the model's commands produce.
type (
	walletMsg   struct{ err error }
	networksMsg struct {
		choices []NetworkChoice
		err     error
	}
	hostKeysMsg struct {
		ip   string
		keys []HostKey
		err  error
	}
	inspectMsg struct {
		results []setup.Inspection
		err     error
	}
	planMsg struct {
		plan *setup.Plan
		err  error
	}
	eventMsg setup.Event
	lineMsg  string
	doneMsg  struct {
		res *setup.Result
		err error
	}
)

// Model is the wizard.
type Model struct {
	svc  Services
	step step
	// history is the steps taken, for going back.
	history []step

	opts  setup.Options
	input input
	// err is shown under the question; it clears on the next key.
	err string

	login       loginMethod
	cursor      int
	hostIPs     []string
	hostKeyIdx  int
	hostKeys    []HostKey
	networks    []NetworkChoice
	toggles     [optCount]bool
	exitAsked   bool
	inspections []setup.Inspection
	plan        *setup.Plan

	events map[string]map[setup.Step]setup.Event
	lines  []string
	result *setup.Result
	runErr error

	walletErr error
	running   bool
	outcome   Outcome
	cancel    context.CancelFunc
	ctx       context.Context
	feed      chan tea.Msg
}

// maxLines is how many lines of the run's output the screen keeps.
const maxLines = 200

// New returns the wizard. ctx ends the run when it is cancelled.
func New(ctx context.Context, svc Services, preset setup.Options) *Model {
	ctx, cancel := context.WithCancel(ctx)
	m := &Model{
		svc: svc, step: stepWallet, opts: preset, ctx: ctx, cancel: cancel,
		events: map[string]map[setup.Step]setup.Event{}, feed: make(chan tea.Msg, feedBuffer),
	}
	m.toggles[optGlobal] = !preset.ClusterOnly
	m.toggles[optRelay] = preset.TorNetwork != ""
	m.toggles[optExit] = preset.Exit
	return m
}

// feedBuffer is how many run events may wait for the screen.
const feedBuffer = 256

// Outcome is how the wizard ended; valid once the program has quit.
func (m *Model) Outcome() Outcome { return m.outcome }

// Init checks the RootWallet first: every step after it signs something.
func (m *Model) Init() tea.Cmd {
	return func() tea.Msg { return walletMsg{err: m.svc.Wallet(m.ctx)} }
}

// Update handles one message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.onKey(msg)
	case walletMsg:
		return m.onWallet(msg)
	case networksMsg:
		return m.onNetworks(msg)
	case hostKeysMsg:
		return m.onHostKeys(msg)
	case inspectMsg:
		return m.onInspect(msg)
	case planMsg:
		return m.onPlan(msg)
	case eventMsg:
		m.record(setup.Event(msg))
		return m, m.wait()
	case lineMsg:
		m.addLine(string(msg))
		return m, m.wait()
	case doneMsg:
		return m.onDone(msg)
	}
	return m, nil
}

func (m *Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.err = ""
	if msg.Type == tea.KeyCtrlC {
		return m.quit(errors.New("interrupted"))
	}
	switch m.step {
	case stepWallet:
		if m.walletErr != nil {
			return m.quit(m.walletErr)
		}
		return m, nil
	case stepInspect:
		return m.onInspectKey(msg)
	case stepRun:
		return m, nil
	case stepDone:
		return m.onDoneKey(msg)
	}
	return m.onStepKey(msg)
}

func (m *Model) quit(err error) (tea.Model, tea.Cmd) {
	m.cancel()
	m.outcome = Outcome{Err: err, Quit: m.step < stepRun}
	return m, tea.Quit
}

// onDoneKey: enter opens `orama status` after a good run; anything ends.
func (m *Model) onDoneKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.outcome = Outcome{Result: m.result, Err: m.runErr, OpenStatus: msg.Type == tea.KeyEnter && m.runErr == nil}
	return m, tea.Quit
}

// onStepKey routes a key to the question being asked.
func (m *Model) onStepKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		return m.back()
	}
	switch m.step {
	case stepIPs, stepUser, stepSecret, stepName, stepStorage, stepTor:
		return m.onInputKey(msg)
	case stepLogin, stepNetwork:
		return m.onListKey(msg)
	case stepOptions:
		return m.onOptionsKey(msg)
	case stepHostKeys:
		return m.onHostKeyKey(msg)
	case stepConfirm:
		return m.onConfirmKey(msg)
	}
	return m, nil
}

// go moves to a step, remembering where it came from, and prepares its input.
func (m *Model) goTo(s step) (tea.Model, tea.Cmd) {
	m.history = append(m.history, m.step)
	return m.enter(s)
}

func (m *Model) back() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		return m, nil
	}
	prev := m.history[len(m.history)-1]
	m.history = m.history[:len(m.history)-1]
	return m.enter(prev)
}

// enter sets up the question of step s.
func (m *Model) enter(s step) (tea.Model, tea.Cmd) {
	m.step = s
	m.cursor = 0
	m.input.SetValue("")
	m.input.Password = false
	m.input.Placeholder = ""
	switch s {
	case stepIPs:
		m.input.Placeholder = "203.0.113.10 203.0.113.11 ..."
		m.input.SetValue(strings.Join(m.opts.IPs, " "))
	case stepUser:
		m.input.SetValue(orDefault(m.opts.User, setup.DefaultSSHUser))
	case stepSecret:
		if m.login == loginTypedPassword {
			m.input.Password = true
		} else {
			m.input.Placeholder = "~/.ssh/id_ed25519"
		}
	case stepName:
		m.input.Placeholder = "alice"
		m.input.SetValue(m.opts.Name)
	case stepStorage:
		m.input.SetValue(orDefaultNum(m.opts.StorageGB, setup.DefaultStorageGB))
	case stepTor:
		m.input.Placeholder = "path to tor-network.json"
		m.input.SetValue(m.opts.TorNetwork)
	case stepNetwork:
		return m, m.loadNetworks()
	case stepHostKeys:
		return m.startHostKeys()
	case stepInspect:
		return m, m.inspect()
	case stepConfirm:
		return m, m.makePlan()
	case stepRun:
		return m.startRun()
	}
	return m, nil
}
