//go:build e2e_fleet

package realistic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// localEnv is the environment a tool run on the runner sees. The run's
// secrets (HCLOUD_TOKEN, CF_API_TOKEN, ...) are in the runner's environment;
// a scanner has no use for them, so they never reach it.
var localEnv = []string{
	"PATH", "HOME", "USER", "LANG", "LC_ALL", "TMPDIR",
	"GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GOFLAGS",
	"PNPM_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
}

// Local is one finished command on the runner.
type Local struct {
	Cmd      string
	Stdout   string
	Stderr   string
	Exit     int
	Duration time.Duration
}

// Output is stdout then stderr.
func (l Local) Output() string { return l.Stdout + l.Stderr }

// Tool returns the path of a tool on the runner's PATH, or skips the test as
// not applicable (not covered) naming what is missing.
func Tool(t testing.TB, name, why string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		harness.SkipNotApplicable(t, name+" is not on the runner's PATH; "+why)
	}
	return path
}

// RunLocal runs name with args in dir on the runner, with the allowlisted
// environment plus extra ("K=V"), and records it as evidence. A non-zero exit
// is returned, not a failure; a command that cannot start or outlives budget
// fails the test.
func RunLocal(t testing.TB, f *fleet.Fleet, dir string, budget time.Duration, extra []string, name string, args ...string) Local {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(allowedEnv(), extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	res := Local{Cmd: name + " " + strings.Join(args, " "), Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		t.Fatalf("%s (in %s) did not finish within %s:\n%s", res.Cmd, dir, budget, f.Redact(Tail(res.Output())))
	case errors.As(err, &exitErr):
		res.Exit = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("failed to run %s in %s: %v", res.Cmd, dir, err)
	}
	record(t, f, res, dir)
	return res
}

func allowedEnv() []string {
	var env []string
	for _, k := range localEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func record(t testing.TB, f *fleet.Fleet, res Local, dir string) {
	t.Helper()
	err := f.Recorder().Add(evidence.Record{Kind: evidence.KindCLI, Test: t.Name(), Summary: "runner: " + res.Cmd + " (in " + dir + ")",
		Status: res.Exit, DurationMS: res.Duration.Milliseconds(), Output: Tail(res.Output())})
	if err != nil {
		t.Errorf("failed to record %s: %v", res.Cmd, err)
	}
}

// tailBytes is how much of a command's output a failure message and the
// evidence record keep: the end, where tools print their verdict.
const tailBytes = 8 << 10

// Tail is the end of s, at most tailBytes.
func Tail(s string) string {
	if len(s) <= tailBytes {
		return s
	}
	return "…" + s[len(s)-tailBytes:]
}
