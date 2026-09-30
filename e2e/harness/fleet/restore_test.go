package fleet

import (
	"context"
	"strings"
	"testing"
)

// TestRestoreNodes_sweepsTaggedRulesAndNTPOnEveryMember: after a
// destructive package every member gets the sweep of this run's tagged
// rules (both families, with -w) and the NTP restore.
func TestRestoreNodes_sweepsTaggedRulesAndNTPOnEveryMember(t *testing.T) {
	sh := &fakeShell{}
	f := newFake(t, sh)
	if err := f.RestoreNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sh.cmds) != len(f.AllNodes()) || len(sh.cmds) == 0 {
		t.Fatalf("%d commands for %d members", len(sh.cmds), len(f.AllNodes()))
	}
	c := sh.cmds[0]
	for _, want := range []string{`e2e-` + f.State.RunID + `-`, `xargs -r -L1 "$t" -w`, "iptables ip6tables", "timedatectl set-ntp true"} {
		if !strings.Contains(c, want) {
			t.Fatalf("restore script lacks %q:\n%s", want, c)
		}
	}
}

func TestRestoreNodes_failuresJoinedAndBadRunIDRefused(t *testing.T) {
	sh := &fakeShell{answer: func(string) (Output, error) { return Output{Exit: 1, Stderr: "iptables still holds rules"}, nil }}
	f := newFake(t, sh)
	if err := f.RestoreNodes(context.Background()); err == nil || !strings.Contains(err.Error(), "still holds") {
		t.Fatalf("err %v", err)
	}
	f.State.RunID = "x'; reboot"
	if err := f.RestoreNodes(context.Background()); err == nil {
		t.Fatal("an unsafe run id reached a root shell")
	}
}
