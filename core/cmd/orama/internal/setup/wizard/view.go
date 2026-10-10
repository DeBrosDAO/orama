package wizard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

var (
	boldStyle  = lipgloss.NewStyle().Bold(true)
	faintStyle = lipgloss.NewStyle().Faint(true)
	errStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

func faint(s string) string { return faintStyle.Render(s) }

// state glyphs: plain characters, readable without colour.
var glyphs = map[setup.State]string{
	setup.StateRunning: "[..]", setup.StateDone: "[ok]", setup.StateSkipped: "[--]", setup.StateFailed: "[!!]",
}

// View draws the current question or the run.
func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(boldStyle.Render("orama setup") + "\n\n")
	b.WriteString(m.body())
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	b.WriteString("\n" + faint(m.hint()) + "\n")
	return b.String()
}

func (m *Model) body() string {
	switch m.step {
	case stepWallet:
		return "Checking that your RootWallet is unlocked..."
	case stepIPs:
		return "Which machines? Paste the IP addresses of the fresh VPSes.\n\n" + m.input.View()
	case stepUser:
		return "Which login do you use on them?\n\n" + m.input.View()
	case stepLogin:
		return "How do you log in to them today?\n\n" + m.list(loginLabels)
	case stepSecret:
		return m.secretBody()
	case stepHostKeys:
		return m.hostKeyBody()
	case stepNetwork:
		return "Which network do you join?\n\n" + m.list(m.networkLabels())
	case stepOptions:
		return m.optionsBody()
	case stepStorage:
		return "How many GB of public storage does each node offer?\n\n" + m.input.View()
	case stepTor:
		return "Where is the Tor network file (tor-network.json) the relay needs?\n\n" + m.input.View()
	case stepName:
		return m.nameBody()
	case stepInspect:
		return m.inspectBody()
	case stepConfirm:
		return m.confirmBody()
	case stepRun, stepDone:
		return m.runBody()
	}
	return ""
}

func (m *Model) secretBody() string {
	if m.login == loginTypedPassword {
		return "Type the machines' password. It is used for this run only and never saved.\n\n" + m.input.View()
	}
	return "Path of the private key that opens the machines:\n\n" + m.input.View()
}

// list draws choices, the cursor's marked.
func (m *Model) list(items []string) string {
	var b strings.Builder
	for i, item := range items {
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		b.WriteString(mark + item + "\n")
	}
	return b.String()
}

func (m *Model) networkLabels() []string {
	labels := make([]string, len(m.networks))
	for i, n := range m.networks {
		labels[i] = fmt.Sprintf("%s  (chain %s)", n.Name, n.ChainID)
	}
	return labels
}

func (m *Model) hostKeyBody() string {
	if len(m.hostKeys) == 0 {
		return "Reading the SSH host key of " + m.currentHost() + "..."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s presents these SSH host keys.\nCompare one with the fingerprint your provider's console shows, then press its number.\n\n", m.currentHost())
	for i, k := range m.hostKeys {
		fmt.Fprintf(&b, "  %d  %-8s %s\n", i+1, k.Type, k.Fingerprint)
	}
	return b.String()
}

func (m *Model) currentHost() string {
	if m.hostKeyIdx < len(m.hostIPs) {
		return m.hostIPs[m.hostKeyIdx]
	}
	return ""
}

func (m *Model) optionsBody() string {
	rows := [optCount]string{
		"Run the global layer: the chain, public storage and a validator",
		"Run a Tor relay (needs the Tor network file)",
		"Make the relay an EXIT relay",
	}
	var b strings.Builder
	b.WriteString("What does each machine do?\n\n")
	for i, row := range rows {
		mark, box := "  ", "[ ]"
		if i == m.cursor {
			mark = "> "
		}
		if m.toggles[i] {
			box = "[x]"
		}
		b.WriteString(mark + box + " " + row + "\n")
	}
	if !m.toggles[optGlobal] {
		b.WriteString("\n" + faint("Without the global layer the machines run only a cluster node (2 vCPU, 2 GiB, 10 GiB).") + "\n")
	}
	if m.exitAsked {
		b.WriteString("\n" + errStyle.Render(setup.ExitWarning) + "\nAccept this? (y/n)\n")
	}
	return b.String()
}

func (m *Model) nameBody() string {
	if m.opts.ClusterOnly {
		return "Name for the machines (optional; several get name-2, name-3, ...):\n\n" + m.input.View()
	}
	return "Name for the node. It is the node's id on the chain; several machines get name-2, name-3, ...\n\n" + m.input.View()
}

func (m *Model) inspectBody() string {
	if m.inspections == nil {
		return "Reaching the machines and reading their hardware..."
	}
	var b strings.Builder
	b.WriteString("The machines:\n\n")
	for _, i := range m.inspections {
		glyph := "[ok]"
		if i.Err != nil {
			glyph = "[!!]"
		}
		b.WriteString("  " + glyph + " " + i.Summary() + "\n")
	}
	return b.String()
}

func (m *Model) confirmBody() string {
	if m.plan == nil {
		return "Planning..."
	}
	var b strings.Builder
	b.WriteString("This is what setup will do:\n\n")
	for _, line := range m.plan.Summary() {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\nStart? (y/n)")
	return b.String()
}

// runBody draws a row per machine with the state of each of its steps, then
// the end of the run's output.
func (m *Model) runBody() string {
	var b strings.Builder
	for _, ip := range m.opts.IPs {
		fmt.Fprintf(&b, "%s\n  %s\n", ip, m.stepLine(ip))
	}
	if run := m.events[""]; len(run) > 0 {
		b.WriteString("run\n  " + m.stepLine("") + "\n")
	}
	b.WriteString("\n")
	for _, line := range m.tail(visibleLines) {
		b.WriteString(faint(line) + "\n")
	}
	if m.step == stepDone {
		b.WriteString("\n" + m.summary())
	}
	return b.String()
}

// visibleLines is how many lines of output the run screen shows.
const visibleLines = 8

func (m *Model) tail(n int) []string {
	if len(m.lines) <= n {
		return m.lines
	}
	return m.lines[len(m.lines)-n:]
}

func (m *Model) stepLine(ip string) string {
	var parts []string
	for _, s := range setup.StepOrder {
		e, ok := m.events[ip][s]
		if !ok {
			continue
		}
		parts = append(parts, glyphs[e.State]+" "+string(s))
	}
	return strings.Join(parts, "  ")
}

func (m *Model) summary() string {
	if m.runErr != nil {
		return errStyle.Render("Setup stopped: "+m.runErr.Error()) + "\nRun the same command again: it resumes where it stopped."
	}
	var b strings.Builder
	b.WriteString(boldStyle.Render("Done.") + "\n")
	if m.result != nil {
		if m.result.Operator != "" {
			b.WriteString("Operator account: " + m.result.Operator + "\n")
		}
		for _, p := range m.result.Pending {
			b.WriteString("Still to do: " + p + "\n")
		}
	}
	return b.String()
}

func (m *Model) hint() string {
	switch m.step {
	case stepWallet:
		return "q quit"
	case stepIPs, stepUser, stepSecret, stepName, stepStorage, stepTor:
		return "enter continue   esc back   ctrl+c quit"
	case stepLogin, stepNetwork:
		return "up/down choose   enter continue   esc back"
	case stepHostKeys:
		return "number: this matches   n: it does not   esc back"
	case stepOptions:
		return "space toggle   enter continue   esc back"
	case stepInspect:
		if m.inspectOK() {
			return "enter continue   esc back   q quit"
		}
		return "esc back   q quit"
	case stepConfirm:
		return "y start   n back"
	case stepRun:
		return "ctrl+c stops after the step in progress"
	case stepDone:
		if m.runErr == nil {
			return "enter: open orama status   any other key: exit"
		}
		return "any key: exit"
	}
	return ""
}
