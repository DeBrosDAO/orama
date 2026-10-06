package upgrade

import (
	"fmt"
	"os"
	"syscall"
)

// reexecAfterBinarySwap replaces this process with the newly-installed
// orama binary at /opt/orama/bin/orama, preserving all original CLI args
// and appending --reexeced-after-binary-swap so the new process knows
// to skip the steps before the swap. Bugboard #15 chicken-and-egg fix.
//
// The node.public_ip the pre-stop step resolved goes along as --public-ip
// (the last one on a command line wins), so the new process records exactly
// what was checked before anything was stopped.
//
// It returns nil without exec'ing when this process already is the installed
// binary — the rolling upgrade runs that one — and otherwise never returns on
// success. Any failure is returned; the caller does not carry on under the
// old code.
func (o *Orchestrator) reexecAfterBinarySwap() error {
	newInfo, err := os.Stat(newOramaBinaryPath)
	if err != nil {
		return fmt.Errorf("new binary not found at %s: %w", newOramaBinaryPath, err)
	}
	cur, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running binary: %w", err)
	}
	curInfo, err := os.Stat(cur)
	if err != nil {
		return fmt.Errorf("stat the running binary %s: %w", cur, err)
	}
	if os.SameFile(curInfo, newInfo) {
		fmt.Printf("  (running the installed binary already; no re-exec needed)\n")
		return nil
	}

	args := reexecArgs(os.Args, o.flags.PublicIP)
	fmt.Printf("\n🔁 Re-executing with newly-installed binary to run remaining phases with current code (#15 fix)...\n")
	// syscall.Exec replaces this process image; argv[0] is the binary
	// path, env inherited as-is. On success we never return.
	if err := syscall.Exec(newOramaBinaryPath, args, os.Environ()); err != nil {
		return fmt.Errorf("syscall.Exec %s: %w", newOramaBinaryPath, err)
	}
	return nil
}

// reexecArgs is the new process's argv: the installed binary, this process's
// arguments, the resolved public IP and the re-exec marker.
func reexecArgs(argv []string, publicIP string) []string {
	args := append([]string{newOramaBinaryPath}, argv[1:]...)
	if publicIP != "" {
		args = append(args, "--public-ip", publicIP)
	}
	return append(args, "--reexeced-after-binary-swap")
}
