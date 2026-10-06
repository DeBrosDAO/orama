// Package oramacli runs the orama CLI under test the way an operator does, but
// in the run's isolated HOME and signing only through the run's throwaway
// RootWallet agent. Every invocation is recorded, redacted, as evidence.
package oramacli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/pace"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// DefaultBudget bounds one invocation when the caller's context has no deadline.
const DefaultBudget = 15 * time.Minute

// Result is one finished invocation.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	Exit     int
	Duration time.Duration
}

// Runner runs one orama binary.
type Runner struct {
	// Bin is the CLI under test; Home is the isolated HOME it runs with.
	Bin  string
	Home string
	// AgentSock is the throwaway agent's socket, passed as RW_AGENT_SOCK.
	AgentSock string
	// Env holds extra KEY=VALUE pairs for every invocation.
	Env []string
	// Recorder receives the evidence; Test names the test it belongs to.
	Recorder *evidence.Recorder
	Test     string
	// RealHome finds the real home directory; secrets.RealHome by default.
	RealHome func() (string, error)
	// GatewayHost is the host of the environment's gateway, which the CLI's
	// credential calls are paced against (ForState sets it).
	GatewayHost string
	// Wallet is the address the agent signs with, for the per-wallet
	// challenge bucket (ForState sets the run's operator address).
	Wallet string
	// Pacer paces the CLI's credential calls; nil means the run's pacer
	// (pace.FromEnv), which is nil outside a fleet run.
	Pacer *pace.Pacer
	// WorkDir is the run's work dir (beside state.json): RunOpts.Dir may be
	// inside it, or inside Home. harness.CLI sets it.
	WorkDir string
	// Target is the run's fleet.State.Target. The stagenet target signs through
	// the dev RootWallet agent, whose socket is in the owner's home (never in
	// ~/.rootwallet); every other target's agent lives outside the home.
	Target string
	// noWallet marks a NoWallet runner: its agent socket must not exist.
	noWallet bool
}

// For returns a copy of r whose evidence is attributed to t.
func (r *Runner) For(t testing.TB) *Runner {
	c := *r
	c.Test = t.Name()
	return &c
}

// Check refuses a runner that could reach the owner's real wallet or config.
func (r *Runner) Check() error {
	realHome, err := r.realHome()
	if err != nil {
		return err
	}
	checkSock := secrets.CheckAgentSockOutsideHome
	if r.Target == config.TargetStagenet {
		checkSock = secrets.CheckAgentSockNotRealWallet
	}
	if err := checkSock(r.AgentSock, realHome); err != nil {
		return fmt.Errorf("refusing to run orama: %w", err)
	}
	if r.Home == "" || !filepath.IsAbs(r.Home) {
		return fmt.Errorf("refusing to run orama: isolated HOME %q must be an absolute path", r.Home)
	}
	if filepath.Clean(r.Home) == filepath.Clean(realHome) {
		return fmt.Errorf("refusing to run orama: HOME is the real home %s", realHome)
	}
	if _, err := os.Stat(r.Bin); err != nil {
		return fmt.Errorf("orama binary under test %q: %w", r.Bin, err)
	}
	return r.checkNoWallet()
}

// checkNoWallet refuses a NoWallet runner whose socket exists: something
// could answer on it, and the CLI would sign instead of asking for approval.
func (r *Runner) checkNoWallet() error {
	if !r.noWallet {
		return nil
	}
	if _, err := os.Lstat(r.AgentSock); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("refusing to run orama: the no-wallet runner's agent socket %s must not exist (stat: %v)", r.AgentSock, err)
	}
	return nil
}

func (r *Runner) realHome() (string, error) {
	if r.RealHome != nil {
		return r.RealHome()
	}
	return secrets.RealHome()
}

// Run executes orama with args. A non-zero exit is reported in Result.Exit,
// not as an error; err means the CLI could not be run or recorded. Commands
// that call the gateway's credential routes are paced first (see pacing.go).
func (r *Runner) Run(ctx context.Context, args ...string) (Result, error) {
	return r.RunWith(ctx, RunOpts{}, args...)
}

// command is the orama invocation of args, in the isolated HOME (or
// opts.Dir) and environment.
func (r *Runner) command(ctx context.Context, opts RunOpts, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Bin, args...)
	cmd.Env, cmd.Dir = append(r.environ(os.LookupEnv), opts.Env...), r.Home
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	isolateGroup(cmd)
	return cmd
}

// finish turns a finished command into a Result: a non-zero exit is Exit,
// only a failure to run is an error.
func finish(res Result, runErr error) (Result, error) {
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.Is(runErr, exec.ErrWaitDelay):
		// The CLI exited; a process it left held its output (see strayOutput).
		res.Exit = 0
	case errors.As(runErr, &exitErr):
		res.Exit = exitErr.ExitCode()
		runErr = nil
	default:
		res.Exit = -1
		runErr = fmt.Errorf("failed to run orama %s: %w", strings.Join(RedactArgs(res.Args), " "), runErr)
	}
	return res, runErr
}

// record writes one invocation; input is what it was given on stdin.
func (r *Runner) record(res Result, input string, runErr error) error {
	rec := evidence.Record{
		Kind: evidence.KindCLI, Test: r.Test, Summary: "orama " + strings.Join(RedactArgs(res.Args), " "),
		Status: res.Exit, DurationMS: res.Duration.Milliseconds(), Output: res.Stdout, Input: input,
	}
	if res.Stderr != "" {
		rec.Output += "\n[stderr]\n" + res.Stderr
	}
	if runErr != nil {
		rec.Error = runErr.Error()
	}
	if err := r.Recorder.Add(rec); err != nil {
		return fmt.Errorf("failed to record the orama invocation: %w", err)
	}
	return nil
}

// inheritedEnv are the only variables the CLI inherits from the runner's
// environment. Everything else — the run's secrets, SSH_AUTH_SOCK (the
// owner's SSH agent), XDG_* (the owner's config), cloud credentials — stays
// out: an allowlist cannot leak a variable nobody thought of.
var inheritedEnv = []string{"PATH", "LANG", "LC_ALL", "TERM", "TMPDIR", "TZ"}

// environ is the allowlisted part of lookup's environment plus the isolated
// HOME, the throwaway agent socket, ORAMA_E2E=1 and Env.
func (r *Runner) environ(lookup func(string) (string, bool)) []string {
	var env []string
	for _, name := range inheritedEnv {
		if v, ok := lookup(name); ok {
			env = append(env, name+"="+v)
		}
	}
	env = append(env, "HOME="+r.Home, config.EnvAgentSock+"="+r.AgentSock, config.EnvE2E+"=1")
	return append(env, r.Env...)
}

// MustOK runs orama and fails the test unless it exits 0.
func (r *Runner) MustOK(t testing.TB, args ...string) Result {
	t.Helper()
	res, err := r.For(t).Run(t.Context(), args...)
	red := r.Recorder.Redactor()
	if err != nil {
		t.Fatal(red.Redact(err.Error()))
	}
	if res.Exit != 0 {
		t.Fatal(red.Redact(fmt.Sprintf("orama %s exited %d\nstdout: %s\nstderr: %s", strings.Join(RedactArgs(args), " "), res.Exit, res.Stdout, res.Stderr)))
	}
	return res
}

// DecodeJSON decodes a result's stdout into v, rejecting unknown trailing
// data. `--json` is not a global output switch: every orama command accepts
// it (a persistent root flag), but only commands that print through the CLI's
// printer honour it; the others ignore it and print text, which this refuses.
func DecodeJSON(res Result, v any) error {
	dec := json.NewDecoder(strings.NewReader(res.Stdout))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("orama %s did not print JSON: %w", strings.Join(RedactArgs(res.Args), " "), err)
	}
	if dec.More() {
		return fmt.Errorf("orama %s printed more than one JSON value", strings.Join(RedactArgs(res.Args), " "))
	}
	return nil
}
