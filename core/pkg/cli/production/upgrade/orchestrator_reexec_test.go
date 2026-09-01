package upgrade

import (
	"os"
	"strings"
	"testing"
)

// Bugboard #15 — Upgrade orchestrator chicken-and-egg.
//
// Pre-fix: Phase 4 (config regen) ran with the pre-swap binary's
// compiled Go code, so config-shape changes shipped in this release
// only took effect on the NEXT rollout. Operators had to upgrade
// twice for a config-changing release to apply.
//
// Post-fix: after Phase 2b installs the new binary, the orchestrator
// re-execs itself using the newly-installed binary so Phase 3+ runs
// with current code. A hidden --reexeced-after-binary-swap flag tells
// the new process to skip the pre-binary phases.
//
// These tests pin the flag plumbing and helper behavior. End-to-end
// re-exec can only be verified on a real install (tests can't safely
// call syscall.Exec).

func TestFlags_ReexecedAfterBinarySwap_parses(t *testing.T) {
	// The hidden flag must be parseable; orchestrator sets it on the
	// re-execed argv. If this regresses (e.g. someone removes the
	// fs.BoolVar registration to clean up the help output), the
	// re-execed process would fail with "flag provided but not defined"
	// and the upgrade would error mid-way.
	flags, err := ParseFlags([]string{"--reexeced-after-binary-swap"})
	if err != nil {
		t.Fatalf("ParseFlags must accept the hidden flag: %v", err)
	}
	if !flags.ReexecedAfterBinarySwap {
		t.Error("flag value not surfaced on Flags struct")
	}
}

func TestFlags_ReexecedAfterBinarySwap_defaultFalse(t *testing.T) {
	// Default value MUST be false. If it ever defaults to true, the
	// orchestrator would skip its own pre-binary phases on the FIRST
	// user-initiated upgrade and bricks would happen — Phase 2b would
	// never run.
	flags, err := ParseFlags([]string{})
	if err != nil {
		t.Fatalf("ParseFlags empty args: %v", err)
	}
	if flags.ReexecedAfterBinarySwap {
		t.Fatal("FATAL DEFAULT: ReexecedAfterBinarySwap defaults to true; this would skip " +
			"Phase 2b (binary install) on every upgrade. MUST be false by default.")
	}
}

func TestReexecAfterBinarySwap_missingBinaryReturnsError(t *testing.T) {
	// When the new binary isn't on disk at the expected path, the
	// helper must surface an error so the orchestrator can fall back
	// (with a warning) rather than silently no-op or panic. This is
	// the "Phase 2b succeeded but the file vanished" case — defensive
	// path, but cheap to pin.
	if _, err := os.Stat(newOramaBinaryPath); err == nil {
		t.Skipf("test machine has %s present; skipping (real install env)", newOramaBinaryPath)
	}
	o := &Orchestrator{flags: &Flags{}}
	err := o.reexecAfterBinarySwap()
	if err == nil {
		t.Error("expected error when new binary path is missing; got nil")
	}
	if err != nil && !strings.Contains(err.Error(), newOramaBinaryPath) {
		t.Errorf("error should mention the missing path %q for operator debuggability; got: %v",
			newOramaBinaryPath, err)
	}
}

func TestReexecPathConstant_isAbsolute(t *testing.T) {
	// syscall.Exec requires an absolute path. If someone refactors the
	// constant to "orama" expecting PATH lookup, the exec call would
	// fail at runtime ONLY in production (test env never reaches
	// syscall.Exec). Pin the absolute-path invariant statically.
	if !strings.HasPrefix(newOramaBinaryPath, "/") {
		t.Fatalf("newOramaBinaryPath must be absolute (syscall.Exec requirement); got %q",
			newOramaBinaryPath)
	}
}
