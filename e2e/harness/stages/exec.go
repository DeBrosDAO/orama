package stages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// StopGrace is how long a cancelled package has to finish: its running tests
// complete and their t.Cleanup restore nodes and delete namespaces. After it
// the whole process group is killed.
const StopGrace = 10 * time.Minute

// stopSignal is what a cancelled package's process group receives. It is
// SIGINT, not SIGTERM: the go command ignores SIGINT and waits for its test
// binary (whose harness.Main stops starting tests and lets the running ones
// clean up), while SIGTERM would kill the go command at once and lose the
// test2json stream with every result of the package.
const stopSignal = syscall.SIGINT

// Command is one process the runner starts.
type Command struct {
	Dir    string
	Env    []string
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	// Budget is how long the command may run before it is stopped like a
	// cancelled one (stopSignal, then SIGKILL after the grace period): the
	// stage timeout. 0 is no budget. The runner hands go test a -timeout of
	// Budget+StopGrace, so the test binary is interrupted (and its cleanups
	// run) instead of panicking on its own timeout, which runs none.
	Budget time.Duration
}

// Executor starts a command and returns its exit code. err is reserved for a
// command that could not be run at all; a non-zero exit is not an error.
type Executor func(ctx context.Context, c Command) (exit int, err error)

// ExecCommand runs c with os/exec in a process group of its own. When ctx is
// cancelled the group gets stopSignal, StopGrace to exit, and then SIGKILL:
// the test binary is a grandchild, so signalling only the go command would
// orphan it.
func ExecCommand(ctx context.Context, c Command) (int, error) {
	return execWithGrace(ctx, c, StopGrace)
}

func execWithGrace(ctx context.Context, c Command, grace time.Duration) (int, error) {
	runCtx, cancel := context.WithCancel(ctx)
	if c.Budget > 0 {
		runCtx, cancel = context.WithTimeout(ctx, c.Budget)
	}
	defer cancel()
	cmd := exec.CommandContext(runCtx, c.Args[0], c.Args[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = c.Dir, c.Env, c.Stdout, c.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return signalGroup(cmd.Process, stopSignal) }
	cmd.WaitDelay = grace
	err := cmd.Run()
	var stopErr error
	if runCtx.Err() != nil && cmd.Process != nil {
		// Whatever outlived the grace period (or the go command) goes now.
		stopErr = signalGroup(cmd.Process, syscall.SIGKILL)
	}
	if ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		stopErr = errors.Join(fmt.Errorf("the package overran its stage timeout of %v and was interrupted: raise the stage timeout in stages.yaml or shorten the package", c.Budget), stopErr)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stopErr
	}
	if err != nil {
		return -1, errors.Join(fmt.Errorf("failed to run %v: %w", c.Args, err), stopErr)
	}
	return 0, stopErr
}

// signalGroup signals the process group p leads. A group that is already
// gone is not an error.
func signalGroup(p *os.Process, sig syscall.Signal) error {
	if err := syscall.Kill(-p.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to send %v to process group %d: %w", sig, p.Pid, err)
	}
	return nil
}
