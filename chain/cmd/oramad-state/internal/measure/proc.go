// Package measure runs oramad as a child process and times what a large state does to it: boot,
// snapshot and restore, genesis export, the indexer's catch-up and block-history pruning. Every
// measurement is a plain struct that the command prints as JSON.
package measure

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Oramad is the oramad binary the measurements drive.
type Oramad struct {
	// Bin is the path of the oramad executable.
	Bin string
}

// Output is what a finished oramad command printed and how long it ran.
type Output struct {
	Stdout, Stderr string
	Elapsed        time.Duration
}

// Combined is both streams: the SDK's commands print their messages to either.
func (o Output) Combined() string { return o.Stdout + o.Stderr }

// Run runs `oramad args...` to completion. A failure carries the tail of its output.
func (o Oramad) Run(ctx context.Context, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, o.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String(), Elapsed: time.Since(start)}
	if err != nil {
		return out, fmt.Errorf("oramad %s failed after %s: %w: %s", strings.Join(args, " "), out.Elapsed.Round(time.Millisecond), err, tail(out.Combined()))
	}
	return out, nil
}

// errorTailBytes is how much of a failed command's output an error carries.
const errorTailBytes = 2000

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > errorTailBytes {
		return "..." + s[len(s)-errorTailBytes:]
	}
	return s
}

// CheckBinary refuses a path that is not an executable file, with a message that says how to build one.
func (o Oramad) CheckBinary() error {
	info, err := os.Stat(o.Bin)
	if err != nil {
		return fmt.Errorf("oramad binary %q: %w (build one with `make build-oramad-state` or pass --oramad)", o.Bin, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return errors.New("oramad binary " + o.Bin + " is not an executable file")
	}
	return nil
}
