package orchardproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// childPIDs lists the children of this test process: from /proc where there is one (a minimal
// container has no pgrep), else with pgrep.
func childPIDs() []string {
	if entries, err := filepath.Glob("/proc/[0-9]*/stat"); err == nil && len(entries) > 0 {
		var pids []string
		self := strconv.Itoa(os.Getpid())
		for _, stat := range entries {
			raw, err := os.ReadFile(stat)
			if err != nil {
				continue
			}
			// pid (comm) state ppid ...: comm may hold spaces and parentheses, so cut after the last ")".
			s := string(raw)
			rest := strings.Fields(s[strings.LastIndex(s, ")")+1:])
			if len(rest) >= 2 && rest[1] == self {
				pids = append(pids, filepath.Base(filepath.Dir(stat)))
			}
		}
		return pids
	}
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// killChildren kills every child of this test process and reports whether it found one.
func killChildren(t *testing.T) bool {
	t.Helper()
	found := false
	for _, pid := range childPIDs() {
		n, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		if err := syscall.Kill(n, syscall.SIGKILL); err == nil {
			found = true
		}
	}
	return found
}
