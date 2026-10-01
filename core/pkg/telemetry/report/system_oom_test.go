package report

import "testing"

func TestCountOOMKills(t *testing.T) {
	tests := []struct {
		name    string
		journal string
		want    int
	}{
		{"empty", "", 0},
		{"no entries marker", "-- No entries --", 0},
		{"unrelated kernel lines", "oom_reaper: reaped process 12 (x), now anon-rss:0kB\nCPU: 1 PID: 4 Comm: x\n", 0},
		{"one global kill", "Out of memory: Killed process 4242 (rqlited) total-vm:1kB, anon-rss:2kB\n", 1},
		{"cgroup and global kills", "Memory cgroup out of memory: Killed process 1 (a) total-vm:1kB\noom-kill:constraint=CONSTRAINT_NONE\nOut of memory: Killed process 2 (b) total-vm:1kB\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CountOOMKills(tt.journal); got != tt.want {
				t.Fatalf("CountOOMKills = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSystemErrors_carriesOOMKillsError(t *testing.T) {
	if got := systemErrors(&SystemReport{OOMKillsError: "journalctl: boom"}); len(got) != 1 || got[0] != "journalctl: boom" {
		t.Fatalf("systemErrors = %v", got)
	}
	if got := systemErrors(&SystemReport{OOMKills: 2}); len(got) != 0 {
		t.Fatalf("known count must not be an error: %v", got)
	}
	if got := systemErrors(nil); len(got) != 0 {
		t.Fatalf("nil system: %v", got)
	}
}
