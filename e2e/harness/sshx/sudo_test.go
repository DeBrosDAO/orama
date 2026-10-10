package sshx

import (
	"os/exec"
	"strings"
	"testing"
)

// A non-root login runs each command as root through sudo; the command survives the quoting intact.
func TestTarget_asRootQuotesTheCommandIntact(t *testing.T) {
	cmd := `printf '%s|%s' "it's" "$((1+1))"`
	if got := (Target{}).asRoot(cmd); got != cmd {
		t.Fatalf("a root login changed the command: %q", got)
	}
	wrapped := (Target{Sudo: true}).asRoot(cmd)
	if !strings.HasPrefix(wrapped, "sudo -n -- bash -c ") {
		t.Fatalf("wrapped = %q", wrapped)
	}
	// Run what sudo would run, without sudo.
	out, err := exec.Command("bash", "-c", strings.TrimPrefix(wrapped, "sudo -n -- ")).CombinedOutput()
	if err != nil || string(out) != "it's|2" {
		t.Fatalf("ran %q: %q %v", wrapped, out, err)
	}
}
