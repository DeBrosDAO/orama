package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Nineteen commands used to set DisableFlagParsing and parse their own
// arguments with the stdlib flag package. Cobra never saw --help for those, so
// it reached the handler as an ordinary argument: `orama node invite --help`
// read /opt/orama/.orama/configs/node.yaml and exited 1 on any machine that is
// not a node — which is exactly where an operator reads help before joining
// one. Others printed a stdlib "Usage of install:" banner with single-dash
// flags, or exited 1 on success.
//
// These walk the real tree so that regression cannot come back quietly.

// walk visits every command in the tree.
func walk(c *cobra.Command, fn func(*cobra.Command)) {
	for _, sub := range c.Commands() {
		fn(sub)
		walk(sub, fn)
	}
}

// commandLine returns the args needed to reach cmd from the root.
func commandLine(cmd *cobra.Command) []string {
	var parts []string
	for c := cmd; c != nil && c.Parent() != nil; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return parts
}

func TestHelpSucceedsForEveryCommand(t *testing.T) {
	walk(newRootCmd(), func(cmd *cobra.Command) {
		path := strings.Join(commandLine(cmd), " ")
		t.Run(path, func(t *testing.T) {
			// A fresh root each time: cobra retains parsed state.
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append(commandLine(cmd), "--help"))

			if err := root.Execute(); err != nil {
				t.Fatalf("`orama %s --help` must succeed, got: %v", path, err)
			}
			if out.Len() == 0 {
				t.Fatalf("`orama %s --help` printed nothing", path)
			}
		})
	})
}

// Cobra owns parsing everywhere, so every command takes GNU-style double-dash
// flags and rejects what it does not define. DisableFlagParsing hands the raw
// argv to a handler instead, which is how single-dash flags and silently
// ignored typos got in.
func TestNoCommandBypassesFlagParsing(t *testing.T) {
	walk(newRootCmd(), func(cmd *cobra.Command) {
		if cmd.DisableFlagParsing {
			t.Errorf("`orama %s` sets DisableFlagParsing: cobra must own parsing so --help works and unknown flags are rejected",
				strings.Join(commandLine(cmd), " "))
		}
	})
}

// An unknown flag must stop the command rather than being ignored. Under the
// old parser a typo could be swallowed and the command would run with defaults.
func TestUnknownFlagIsRejected(t *testing.T) {
	for _, path := range [][]string{
		{"node", "install"},
		{"node", "upgrade"},
		{"node", "push"},
		{"node", "rollout"},
		{"node", "decommission"},
		{"build"},
		{"inspect"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append(path, "--definitely-not-a-flag"))

			if err := root.Execute(); err == nil {
				t.Fatal("an unknown flag must be an error")
			}
		})
	}
}
