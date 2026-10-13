package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// caffeinateBin is macOS's tool for holding a power assertion.
const caffeinateBin = "caffeinate"

// keepAwakeArgs is the caffeinate command line that keeps the host awake for as
// long as process pid lives, nil on a system with no such tool. -i blocks idle
// sleep, -s system sleep on AC power; -w ends the assertion with pid, so a
// crashed runner never leaves the machine pinned awake.
//
// A laptop that sleeps mid-run breaks every connection and deadline its tests
// hold and turns them into false failures; the stage runner reports any sleep
// it could not prevent (lid closed on battery) on the package it hit.
func keepAwakeArgs(goos string, pid int) []string {
	if goos != "darwin" {
		return nil
	}
	return []string{caffeinateBin, "-i", "-s", "-w", strconv.Itoa(pid)}
}

// keepAwake holds the host awake until the returned stop is called.
func keepAwake(goos string, lookPath func(string) (string, error)) (stop func(), err error) {
	args := keepAwakeArgs(goos, os.Getpid())
	if args == nil {
		return func() {}, nil
	}
	bin, err := lookPath(args[0])
	if err != nil {
		return nil, fmt.Errorf("failed to find %s, which keeps this Mac awake during the run: %w", args[0], err)
	}
	cmd := exec.Command(bin, args[1:]...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s to keep this Mac awake during the run: %w", args[0], err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}, nil
}
