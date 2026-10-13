package main

import (
	"errors"
	"strings"
	"testing"
)

func lookPathOK(name string) (string, error) { return "/usr/bin/" + name, nil }

func TestSandboxPrefix_offByDefault(t *testing.T) {
	got, err := sandboxPrefix(env(map[string]string{}), "linux", "/home/o", nil, lookPathOK)
	if got != nil || err != nil {
		t.Fatalf("got %v err %v", got, err)
	}
}

// TestSandboxPrefix_hidesTheRealHomeKeepsTheRunsPaths: the real home is
// an empty tmpfs; only the run's paths under it are bound back.
func TestSandboxPrefix_hidesTheRealHomeKeepsTheRunsPaths(t *testing.T) {
	lookup := env(map[string]string{EnvSandbox: "1"})
	got, err := sandboxPrefix(lookup, "linux", "/home/o", []string{"/home/o/dev/orama", "/tmp/w", "/home/o/go/pkg/mod"}, lookPathOK)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"/usr/bin/bwrap", "--tmpfs /home/o", "--bind /home/o/dev/orama /home/o/dev/orama", "--bind /home/o/go/pkg/mod /home/o/go/pkg/mod"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q missing from %q", want, joined)
		}
	}
	if strings.Contains(joined, "/tmp/w") || got[len(got)-1] != "--" {
		t.Fatalf("prefix %q", joined)
	}
}

func TestSandboxPrefix_refusedWhereItCannotWork(t *testing.T) {
	lookup := env(map[string]string{EnvSandbox: "1"})
	if _, err := sandboxPrefix(lookup, "darwin", "/Users/o", nil, lookPathOK); err == nil {
		t.Fatal("a sandbox was promised on macOS")
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := sandboxPrefix(lookup, "linux", "/home/o", nil, missing); err == nil {
		t.Fatal("a sandbox was promised without bwrap")
	}
}
