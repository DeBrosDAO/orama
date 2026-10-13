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

// TestRestoreNodes_removesTheChainFilesOfThisRunAndReportsAnUnrestoredBackup:
// an interrupted chain package leaves its disk filler and throwaway keyrings;
// the sweep deletes exactly those named for this run, and an old chain data
// directory a state-sync test set aside is reported with the steps to put it
// back, never deleted.
func TestRestoreNodes_removesTheChainFilesOfThisRunAndReportsAnUnrestoredBackup(t *testing.T) {
	sh := &fakeShell{}
	f := newFake(t, sh)
	if err := f.RestoreNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	run := f.State.RunID
	c := sh.cmds[0]
	for _, want := range []string{
		"-name 'e2e-chaindisk-" + run + "-*'", "-name 'e2e-chainkeys-" + run + "-*'", "-name 'e2e-chaintx-" + run + "-*'",
		"-xdev -maxdepth 1", "/var/lib/orama-global/chain/data.e2e-" + run, "UNRESTORED STATE-SYNC BACKUP", "mv /var/lib/orama-global/chain/data.e2e-" + run,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("restore script lacks %q:\n%s", want, c)
		}
	}
	if strings.Contains(c, "rm -rf /var/lib/orama-global/chain/data.e2e") {
		t.Error("the sweep deletes the state-sync backup")
	}
}
