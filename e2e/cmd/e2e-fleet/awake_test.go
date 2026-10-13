package main

import (
	"errors"
	"slices"
	"testing"
)

func TestKeepAwakeArgs_darwinHoldsWhileTheRunnerLives(t *testing.T) {
	got := keepAwakeArgs("darwin", 4242)
	want := []string{"caffeinate", "-i", "-s", "-w", "4242"}
	if !slices.Equal(got, want) {
		t.Fatalf("keepAwakeArgs(darwin) = %v, want %v", got, want)
	}
}

func TestKeepAwakeArgs_otherSystemsHoldNothing(t *testing.T) {
	for _, goos := range []string{"linux", "freebsd", ""} {
		if got := keepAwakeArgs(goos, 1); got != nil {
			t.Errorf("keepAwakeArgs(%q) = %v, want nil", goos, got)
		}
	}
}

func TestKeepAwake_missingToolIsAnError(t *testing.T) {
	notFound := func(string) (string, error) { return "", errors.New("not found") }
	if _, err := keepAwake("darwin", notFound); err == nil {
		t.Fatal("a Mac without caffeinate started the run with nothing keeping it awake")
	}
	stop, err := keepAwake("linux", notFound)
	if err != nil {
		t.Fatalf("linux needs no tool, got %v", err)
	}
	stop()
}
