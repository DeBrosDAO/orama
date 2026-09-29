package orchardproc

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// killChildren kills every child of this test process and reports whether it found one.
func killChildren(t *testing.T) bool {
	t.Helper()
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return false
	}
	found := false
	for _, pid := range strings.Fields(string(out)) {
		if err := exec.Command("kill", "-9", pid).Run(); err == nil {
			found = true
		}
	}
	return found
}
