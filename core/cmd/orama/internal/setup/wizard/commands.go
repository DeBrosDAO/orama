package wizard

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

func (m *Model) loadNetworks() tea.Cmd {
	return func() tea.Msg {
		choices, err := m.svc.Networks()
		return networksMsg{choices: choices, err: err}
	}
}

func (m *Model) scan(ip string) tea.Cmd {
	return func() tea.Msg {
		keys, err := m.svc.HostKeys(m.ctx, ip)
		return hostKeysMsg{ip: ip, keys: keys, err: err}
	}
}

func (m *Model) inspect() tea.Cmd {
	opts := m.opts
	return func() tea.Msg {
		results, err := m.svc.Inspect(m.ctx, opts)
		return inspectMsg{results: results, err: err}
	}
}

func (m *Model) makePlan() tea.Cmd {
	opts := m.opts
	return func() tea.Msg {
		plan, err := m.svc.Plan(m.ctx, opts)
		return planMsg{plan: plan, err: err}
	}
}

// startRun runs the setup in the background; its events and output come back
// through the feed channel one message at a time.
func (m *Model) startRun() (tea.Model, tea.Cmd) {
	opts := m.opts
	opts.Yes = true
	m.running = true
	go func() {
		res, err := m.svc.Run(m.ctx, opts, chanReporter{feed: m.feed})
		m.feed <- doneMsg{res: res, err: err}
	}()
	return m, m.wait()
}

// wait blocks until the run sends its next message.
func (m *Model) wait() tea.Cmd {
	return func() tea.Msg { return <-m.feed }
}

// Feed lets the program that owns the terminal send the run's printed output to
// the screen. A line that finds the queue full is dropped: it is the output of a
// legacy print, the run's own events are never dropped, and a full queue means
// the screen is gone.
func (m *Model) Feed(line string) {
	select {
	case m.feed <- lineMsg(line):
	default:
	}
}

func (m *Model) onDone(msg doneMsg) (tea.Model, tea.Cmd) {
	m.step, m.result, m.runErr, m.running = stepDone, msg.res, msg.err, false
	return m, nil
}

func (m *Model) record(e setup.Event) {
	byStep := m.events[e.Node]
	if byStep == nil {
		byStep = map[setup.Step]setup.Event{}
		m.events[e.Node] = byStep
	}
	byStep[e.Step] = e
}

func (m *Model) addLine(line string) {
	m.lines = append(m.lines, line)
	if len(m.lines) > maxLines {
		m.lines = m.lines[len(m.lines)-maxLines:]
	}
}

// chanReporter hands a run's events and lines to the screen.
type chanReporter struct{ feed chan<- tea.Msg }

func (c chanReporter) Emit(e setup.Event) { c.feed <- eventMsg(e) }
func (c chanReporter) Linef(format string, args ...any) {
	c.feed <- lineMsg(fmt.Sprintf(format, args...))
}
