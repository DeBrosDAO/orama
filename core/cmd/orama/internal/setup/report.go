package setup

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Step is one thing a run does to a machine, in the order it does them.
type Step string

// The steps. A machine goes through the ones its plan has.
const (
	StepEnroll   Step = "enroll"
	StepHardware Step = "hardware"
	StepRelease  Step = "release"
	StepCluster  Step = "cluster"
	StepGlobal   Step = "global"
	StepGenesis  Step = "genesis"
	StepSync     Step = "sync"
	StepRestart  Step = "restart"
	StepOnchain  Step = "onchain"
	StepName     Step = "name"
	StepDNS      Step = "dns"
)

// StepOrder lists the steps in the order a run takes them.
var StepOrder = []Step{StepEnroll, StepHardware, StepRelease, StepCluster, StepGlobal, StepGenesis, StepSync, StepRestart, StepOnchain, StepName, StepDNS}

// State is how a step is going.
type State string

const (
	StateRunning State = "running"
	StateDone    State = "done"
	// StateSkipped says the machine already has what the step makes.
	StateSkipped State = "skipped"
	StateFailed  State = "failed"
)

// Event is a step of one machine changing state. Node is the machine's IP, or
// empty for a step of the run as a whole.
type Event struct {
	Node   string
	Step   Step
	State  State
	Detail string
}

// Reporter receives the events of a run, from several goroutines.
type Reporter interface {
	Emit(Event)
	// Linef is a line of the run's own: a result, a record to create.
	Linef(format string, args ...any)
}

// CleanTerminal replaces what could drive the operator's terminal, or make text
// read differently from how it is stored, in text a machine, a seed or a chain
// node produced: control characters (an escape sequence can rewrite the screen,
// set the window title or write the clipboard), every Unicode format character
// (the bidirectional overrides and isolates, the direction and zero-width marks,
// the byte-order mark, the tag block), the line and paragraph separators, and
// bytes that are not UTF-8. Line feeds and tabs stay; use oneLine where a value
// must not be able to start another line.
func CleanTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r), r == unicode.ReplacementChar:
			return '?'
		}
		return r
	}, s)
}

// NewTerminalWriter returns a writer that passes w what is written to it with
// CleanTerminal applied, as it arrives: a prompt without a newline is shown at
// once. A multi-byte character split across two writes is held back until it is
// whole.
func NewTerminalWriter(w io.Writer) io.Writer { return &terminalWriter{w: w} }

type terminalWriter struct {
	w    io.Writer
	tail []byte
}

func (t *terminalWriter) Write(p []byte) (int, error) {
	data := append(t.tail, p...)
	cut := wholeRunes(data)
	if _, err := io.WriteString(t.w, CleanTerminal(string(data[:cut]))); err != nil {
		return 0, err
	}
	t.tail = append([]byte(nil), data[cut:]...)
	return len(p), nil
}

// wholeRunes is the length of the longest prefix of data that does not end in
// the middle of a UTF-8 sequence.
func wholeRunes(data []byte) int {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if utf8.FullRune(data[i:]) {
				return len(data)
			}
			return i
		}
	}
	return len(data)
}

// oneLine is s cleaned and on one line: a detail printed after a step's name must
// not be able to start another line of the log.
func oneLine(s string) string {
	return strings.ReplaceAll(CleanTerminal(s), "\n", " | ")
}

// TextReporter prints a run as lines.
type TextReporter struct {
	mu  sync.Mutex
	Out io.Writer
}

// Emit prints one line per state change except the start of a step.
func (t *TextReporter) Emit(e Event) {
	if e.State == StateRunning && e.Detail == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	where := e.Node
	if where == "" {
		where = "setup"
	}
	suffix := ""
	if e.Detail != "" {
		suffix = ": " + oneLine(e.Detail)
	}
	fmt.Fprintf(t.Out, "[%s] %s %s%s\n", where, e.Step, e.State, suffix)
}

// Linef prints a line.
func (t *TextReporter) Linef(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintln(t.Out, CleanTerminal(fmt.Sprintf(format, args...)))
}
