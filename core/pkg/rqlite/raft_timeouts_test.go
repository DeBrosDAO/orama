package rqlite

import (
	"strings"
	"testing"
)

// A namespace instance started with only "-auth ..." ran on rqlite's LAN
// defaults and held an election every few seconds; it now gets every platform
// timing flag.
func TestWithDefaultRaftTimeouts_fillsEveryFlag(t *testing.T) {
	got := WithDefaultRaftTimeouts("")
	for _, want := range []string{
		"-raft-election-timeout 5s", "-raft-heartbeat-timeout 2s",
		"-raft-apply-timeout 30s", "-raft-leader-lease-timeout 2s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

// A flag the caller set — the index's, from the node config — is kept, in
// either spelling, and not given a second value.
func TestWithDefaultRaftTimeouts_keepsCallerFlags(t *testing.T) {
	for _, extra := range []string{"-raft-heartbeat-timeout 7s", "-raft-heartbeat-timeout=7s"} {
		got := WithDefaultRaftTimeouts(extra + " -node-id x")
		if strings.Count(got, "-raft-heartbeat-timeout") != 1 || !strings.Contains(got, "7s") {
			t.Errorf("WithDefaultRaftTimeouts(%q) = %q, want the caller's heartbeat once", extra, got)
		}
		if !strings.Contains(got, "-node-id x") || !strings.Contains(got, "-raft-election-timeout 5s") {
			t.Errorf("WithDefaultRaftTimeouts(%q) = %q, lost an argument or a default", extra, got)
		}
	}
}

func TestRaftTimeouts_args(t *testing.T) {
	got := RaftTimeouts{Election: 1e9, Heartbeat: 2e9, Apply: 3e9, LeaderLease: 4e9}.Args()
	want := "-raft-election-timeout 1s -raft-heartbeat-timeout 2s -raft-apply-timeout 3s -raft-leader-lease-timeout 4s"
	if got != want {
		t.Errorf("Args() = %q, want %q", got, want)
	}
}
