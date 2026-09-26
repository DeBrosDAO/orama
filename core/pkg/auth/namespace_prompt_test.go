package auth

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// `echo "" | orama auth login` and a CI job both used to fail with
// "failed to read namespace: EOF": the prompt read stdin whether or not a
// person was there to answer it.
func TestPromptNamespace_withoutATerminalSignsInWithoutOne(t *testing.T) {
	var out bytes.Buffer
	ns, err := promptNamespace(bufio.NewReader(strings.NewReader("")), &out, false)
	if err != nil || ns != "" {
		t.Fatalf("promptNamespace = %q, %v; want \"\", nil", ns, err)
	}
	if out.Len() != 0 {
		t.Errorf("prompted with no terminal to answer: %q", out.String())
	}
}

func TestPromptNamespace_readsTheAnswer(t *testing.T) {
	for input, want := range map[string]string{"  myapp \n": "myapp", "\n": ""} {
		var out bytes.Buffer
		ns, err := promptNamespace(bufio.NewReader(strings.NewReader(input)), &out, true)
		if err != nil || ns != want {
			t.Errorf("input %q: promptNamespace = %q, %v; want %q", input, ns, err, want)
		}
		if !strings.Contains(out.String(), "Enter namespace") {
			t.Errorf("input %q: no prompt shown", input)
		}
	}
}

// A person who closes stdin at the prompt (Ctrl-D) did not answer.
func TestPromptNamespace_terminalClosedWithoutAnswer(t *testing.T) {
	var out bytes.Buffer
	if _, err := promptNamespace(bufio.NewReader(strings.NewReader("")), &out, true); err == nil {
		t.Fatal("EOF at an interactive prompt was taken as an answer")
	}
}
