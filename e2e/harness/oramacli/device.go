package oramacli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noWalletSock is the agent socket of a NoWallet runner, under its isolated
// HOME. It is never created: the CLI finds no wallet there.
const noWalletSock = ".no-wallet/agent.sock"

// Lines of the device login's output (core/cmd/orama/internal/auth_commands.go
// deviceLogin): the code, then the command that approves it.
const (
	userCodeMarker = "Your code:"
	approveMarker  = "orama auth approve "
)

// userCodeBudget bounds the wait for the device login to print its code.
const userCodeBudget = 2 * time.Minute

// NoWallet returns a runner for a machine with no wallet: a fresh isolated
// HOME (as Isolated) and RW_AGENT_SOCK pointing at a socket inside it that
// does not exist, never the real one and never empty. The CLI then finds no
// agent, and `orama auth login` falls back to the device login (a code
// another machine approves). Check refuses the runner if the socket appears.
func (r *Runner) NoWallet(t testing.TB) *Runner {
	t.Helper()
	c := r.Isolated(t)
	c.AgentSock = filepath.Join(c.Home, noWalletSock)
	c.Wallet = ""
	c.noWallet = true
	return c
}

// PendingLogin is a device login waiting for approval (DeviceLogin).
type PendingLogin struct {
	// UserCode is the code the CLI printed, what `orama auth approve` takes.
	UserCode string
	// Namespace is the namespace the login asked for ("" for none).
	Namespace string
	// Proc is the waiting `orama auth login`.
	Proc *Proc
}

// DeviceLogin starts `orama auth login [--namespace ns]` on a NoWallet runner
// and returns once the CLI has printed its user code (and the matching
// `orama auth approve <code>` line). Approve it from a runner that has a
// wallet, then Wait. A cleanup kills and reaps the login if the test does not.
func DeviceLogin(t testing.TB, r *Runner, namespace string) *PendingLogin {
	t.Helper()
	if !r.noWallet {
		t.Fatal("oramacli.DeviceLogin needs a runner from Runner.NoWallet: with a wallet the CLI signs in directly and prints no code")
	}
	args := []string{"auth", "login"}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	p, err := r.For(t).Start(t.Context(), args...)
	red := r.Recorder.Redactor()
	if err != nil {
		t.Fatal(red.Redact(err.Error()))
	}
	t.Cleanup(func() { reap(t, p) })
	code, err := awaitUserCode(t.Context(), p)
	if err != nil {
		reap(t, p)
		res, _ := p.Wait()
		t.Fatal(red.Redact(fmt.Sprintf("%v\nstdout: %s\nstderr: %s", err, res.Stdout, res.Stderr)))
	}
	return &PendingLogin{UserCode: code, Namespace: namespace, Proc: p}
}

// reap kills p (a no-op once it exited) and waits for it.
func reap(t testing.TB, p *Proc) {
	t.Helper()
	if err := p.Kill(); err != nil {
		t.Error(err)
	}
	if _, err := p.Wait(); err != nil {
		t.Error(p.r.Recorder.Redactor().Redact(err.Error()))
	}
}

// awaitUserCode reads p's stdout until the code and its approve line appear.
func awaitUserCode(ctx context.Context, p *Proc) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, userCodeBudget)
	defer cancel()
	var code string
	for {
		select {
		case line, ok := <-p.StdoutLines():
			if !ok {
				return "", fmt.Errorf("orama auth login ended before printing a user code and its approve command")
			}
			var done bool
			var err error
			if code, done, err = parseLoginLine(code, line); done || err != nil {
				return code, err
			}
		case <-ctx.Done():
			return "", fmt.Errorf("orama auth login printed no user code within %s: %w", userCodeBudget, ctx.Err())
		}
	}
}

// parseLoginLine reads one line of the device login's output given the code
// seen so far; done once the approve line confirms the code.
func parseLoginLine(code, line string) (string, bool, error) {
	line = strings.TrimSpace(line)
	if rest, ok := strings.CutPrefix(line, userCodeMarker); ok {
		c := strings.TrimSpace(rest)
		if c == "" || strings.ContainsAny(c, " \t") {
			return "", false, fmt.Errorf("orama auth login printed a malformed user code line %q", line)
		}
		return c, false, nil
	}
	rest, ok := strings.CutPrefix(line, approveMarker)
	if !ok || code == "" {
		return code, false, nil
	}
	if strings.TrimSpace(rest) != code {
		return "", false, fmt.Errorf("orama auth login printed code %q but told to approve %q", code, strings.TrimSpace(rest))
	}
	return code, true, nil
}

// ApproveArgs are the arguments of `orama auth approve` for this login
// (append "--deny" to refuse it instead).
func (l *PendingLogin) ApproveArgs() []string {
	args := []string{"auth", "approve", l.UserCode}
	if l.Namespace != "" {
		args = append(args, "--namespace", l.Namespace)
	}
	return args
}

// Approve runs `orama auth approve <code>` as approver (a runner with a
// wallet, e.g. harness.CLI(t) or its Isolated copy) and requires exit 0.
func (l *PendingLogin) Approve(t testing.TB, approver *Runner) Result {
	t.Helper()
	return approver.MustOK(t, l.ApproveArgs()...)
}

// Deny refuses the login with `orama auth approve <code> --deny`.
func (l *PendingLogin) Deny(t testing.TB, approver *Runner) Result {
	t.Helper()
	return approver.MustOK(t, append(l.ApproveArgs(), "--deny")...)
}

// Wait waits for the login to end and returns its Result (exit 0 once
// approved, non-zero once denied or expired). It fails the test only when the
// CLI could not be run or recorded.
func (l *PendingLogin) Wait(t testing.TB) Result {
	t.Helper()
	res, err := l.Proc.Wait()
	if err != nil {
		t.Fatal(l.Proc.r.Recorder.Redactor().Redact(err.Error()))
	}
	return res
}
