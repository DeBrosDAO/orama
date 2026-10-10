package setup

import (
	"fmt"
	"io"
	"sync"
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
	StepSync     Step = "sync"
	StepRestart  Step = "restart"
	StepOnchain  Step = "onchain"
	StepName     Step = "name"
	StepDNS      Step = "dns"
)

// StepOrder lists the steps in the order a run takes them.
var StepOrder = []Step{StepEnroll, StepHardware, StepRelease, StepCluster, StepGlobal, StepSync, StepRestart, StepOnchain, StepName, StepDNS}

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
		suffix = ": " + e.Detail
	}
	fmt.Fprintf(t.Out, "[%s] %s %s%s\n", where, e.Step, e.State, suffix)
}

// Linef prints a line.
func (t *TextReporter) Linef(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintf(t.Out, format+"\n", args...)
}
