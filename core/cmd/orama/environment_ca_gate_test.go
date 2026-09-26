package main

import (
	"strings"
	"testing"
)

// A missing CA file used to fail every command, including `orama env add
// --ca-file`, the one its error message tells you to run.
func TestNeedsEnvironmentCAs(t *testing.T) {
	root := newRootCmd()
	for args, want := range map[string]bool{
		"env add":        false,
		"env remove":     false,
		"env list":       false,
		"version":        false,
		"auth login":     true,
		"node wipe":      true,
		"namespace list": true,
	} {
		cmd, _, err := root.Find(strings.Fields(args))
		if err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		if got := needsEnvironmentCAs(cmd); got != want {
			t.Errorf("needsEnvironmentCAs(%q) = %v, want %v", args, got, want)
		}
	}
}
