package setup

import (
	"bytes"
	"strings"
	"testing"
)

func TestCleanTerminal(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":             {"orama setup", "orama setup"},
		"tabs and newlines": {"a\tb\nc", "a\tb\nc"},
		"an escape":         {"x\x1b[2Jy", "x?[2Jy"},
		"a title":           {"\x1b]0;owned\x07", "?]0;owned?"},
		"c1 controls":       {"a\u009bb", "a?b"},
		"a bidi override":   {"a‮b", "a?b"},
		"an isolate":        {"a⁦b", "a?b"},
		"invalid utf-8":     {"a\xffb", "a?b"},
		"a carriage return": {"a\rb", "a?b"},
		"unicode text":      {"héllo wörld", "héllo wörld"},
	} {
		if got := CleanTerminal(tc.in); got != tc.want {
			t.Errorf("%s: CleanTerminal(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

func TestTextReporter_neverPassesAMachinesEscapesThrough(t *testing.T) {
	var out bytes.Buffer
	r := &TextReporter{Out: &out}
	r.Emit(Event{Node: "203.0.113.5", Step: StepCluster, State: StateFailed, Detail: "boom\x1b]52;c;ZXZpbA==\x07"})
	r.Linef("chain says: %s", "\x1b[2J")
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("control characters reached the terminal: %q", out.String())
	}
	if !strings.Contains(out.String(), "[203.0.113.5] cluster failed: boom") {
		t.Errorf("output %q", out.String())
	}
}

func TestTextReporter_aStartWithoutDetailIsNotALine(t *testing.T) {
	var out bytes.Buffer
	r := &TextReporter{Out: &out}
	r.Emit(Event{Step: StepEnroll, State: StateRunning})
	if out.Len() != 0 {
		t.Errorf("printed %q", out.String())
	}
	r.Emit(Event{Step: StepDNS, State: StateDone, Detail: "cluster.example.org"})
	if !strings.Contains(out.String(), "[setup] dns done: cluster.example.org") {
		t.Errorf("a step of the run as a whole is attributed to setup: %q", out.String())
	}
}
