package evidence

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// KindExec is a local subprocess that is not the CLI under test (vitest,
// go build, tsx, a scanner).
const KindExec = "exec"

// ExecResult is one finished subprocess.
type ExecResult struct {
	Stdout   string
	Stderr   string
	Exit     int
	Duration time.Duration
}

// RunRecorded runs cmd (built, not started; Stdout and Stderr unset) to the
// end and records it as evidence attributed to t: name, the command line,
// the working directory, the exit status, the duration and the output, all
// redacted and bounded like every record. A non-zero exit is ExecResult.Exit,
// not an error; err means it could not be run or recorded. rec may be nil
// (nothing is recorded).
func RunRecorded(t testing.TB, rec *Recorder, name string, cmd *exec.Cmd) (ExecResult, error) {
	t.Helper()
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return ExecResult{Exit: -1}, fmt.Errorf("%s: RunRecorded captures the output itself; leave cmd.Stdout and cmd.Stderr unset", name)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		res.Exit, runErr = exitErr.ExitCode(), nil
	default:
		res.Exit = -1
		runErr = fmt.Errorf("%s: failed to run %s: %w", name, cmd.Path, runErr)
	}
	r := Record{Kind: KindExec, Test: t.Name(), Summary: name + ": " + strings.Join(cmd.Args, " "),
		Status: res.Exit, DurationMS: res.Duration.Milliseconds(), Input: "dir: " + cmd.Dir,
		Output: res.Stdout + stderrPart(res.Stderr)}
	if runErr != nil {
		r.Error = runErr.Error()
	}
	if err := rec.Add(r); err != nil {
		return res, errors.Join(runErr, fmt.Errorf("%s: failed to record: %w", name, err))
	}
	return res, runErr
}

func stderrPart(stderr string) string {
	if stderr == "" {
		return ""
	}
	return "\n[stderr]\n" + stderr
}
