package oramacli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// cliWaitDelay is how long a finished orama invocation's output may stay
// open: a child it left behind holding stdout or stderr gets this long
// before its process group is killed and the pipes are closed. A variable so
// tests can shorten it.
var cliWaitDelay = 10 * time.Second

// isolateGroup runs cmd in a process group of its own: ending the context
// kills the whole group (a helper the CLI started dies with it), and Wait
// gives up on inherited output after cliWaitDelay.
func isolateGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd.Process) }
	cmd.WaitDelay = cliWaitDelay
}

// killGroup SIGKILLs the process group p leads; a group already gone is not
// an error. The group is ours (same uid, created with Setpgid), so EPERM
// only means its members are all exited: macOS answers EPERM, not ESRCH,
// for a group left with zombies only.
func killGroup(p *os.Process) error {
	if p == nil {
		return nil
	}
	err := syscall.Kill(-p.Pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("failed to kill process group %d: %w", p.Pid, err)
	}
	return nil
}

// strayOutput reports a child of the CLI that kept its output open after
// it exited, and kills its group.
func strayOutput(cmd *exec.Cmd, args []string) error {
	return errors.Join(fmt.Errorf("orama %s exited but a process it started kept its output open for %s: its process group was killed",
		strings.Join(RedactArgs(args), " "), cliWaitDelay), killGroup(cmd.Process))
}
