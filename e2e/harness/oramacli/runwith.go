package oramacli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// RunOpts are what one invocation adds to the runner's isolation. The
// allowlisted environment and the checks of Run still apply.
type RunOpts struct {
	// Stdin is written to the CLI's stdin and recorded (redacted) with the
	// invocation: typed confirmations, 'y' to a prompt.
	Stdin []byte
	// StdinReader streams to stdin instead (StartWith, answering a prompt
	// once it appears); it is not recorded. Set at most one of the two.
	StdinReader io.Reader
	// Dir is the working directory (default Home). It must be inside Home or
	// inside the run's work dir (Runner.WorkDir), symlinks resolved.
	Dir string
	// Env adds KEY=VALUE pairs after the allowlist (see GoEnv). HOME,
	// RW_AGENT_SOCK, ORAMA_E2E, SSH_AUTH_SOCK, XDG_* and every run secret
	// are refused.
	Env []string
}

// goEnvNames are the Go toolchain variables GoEnv passes through.
var goEnvNames = []string{"GOFLAGS", "GOCACHE", "GOMODCACHE", "GOPATH", "GOTOOLCHAIN", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOPROXY", "GOSUMDB"}

// GoEnv is the Go toolchain part of lookup's environment (os.LookupEnv),
// for RunOpts.Env of a command that builds (`orama maint build`).
func GoEnv(lookup func(string) (string, bool)) []string {
	var env []string
	for _, name := range goEnvNames {
		if v, ok := lookup(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// reservedEnv are names RunOpts.Env may not set: the isolation itself.
var reservedEnv = []string{"HOME", config.EnvAgentSock, config.EnvE2E, "SSH_AUTH_SOCK"}

// RunWith is Run with opts.
func (r *Runner) RunWith(ctx context.Context, opts RunOpts, args ...string) (Result, error) {
	if err := errors.Join(r.Check(), r.checkOpts(opts)); err != nil {
		return Result{Args: args, Exit: -1}, err
	}
	if opts.StdinReader != nil {
		return Result{Args: args, Exit: -1}, errors.New("RunOpts.StdinReader is for StartWith; give Run its input as Stdin")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultBudget)
		defer cancel()
	}
	plan, err := r.paceBefore(ctx, args, opts.Env)
	if err != nil {
		return Result{Args: args, Exit: -1}, err
	}
	var stdout, stderr bytes.Buffer
	cmd := r.command(ctx, opts, args)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.stdin(), &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	var stray error
	if errors.Is(runErr, exec.ErrWaitDelay) {
		stray = strayOutput(cmd, args)
	}
	res, runErr := finish(Result{Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}, runErr)
	runErr = errors.Join(runErr, stray, plan.observe(stdout.Bytes()), plan.after())
	if err := r.record(res, string(opts.Stdin), runErr); err != nil {
		return res, errors.Join(runErr, err)
	}
	return res, runErr
}

func (o RunOpts) stdin() io.Reader {
	if o.StdinReader != nil {
		return o.StdinReader
	}
	if o.Stdin != nil {
		return bytes.NewReader(o.Stdin)
	}
	return nil
}

// checkOpts refuses a working directory outside Home and the work dir, and
// an environment entry that would undo the isolation.
func (r *Runner) checkOpts(o RunOpts) error {
	if o.Stdin != nil && o.StdinReader != nil {
		return errors.New("refusing to run orama: set RunOpts.Stdin or RunOpts.StdinReader, not both")
	}
	for _, kv := range o.Env {
		if err := checkEnvEntry(kv); err != nil {
			return err
		}
	}
	if o.Dir == "" {
		return nil
	}
	return r.checkDir(o.Dir)
}

func checkEnvEntry(kv string) error {
	name, _, ok := strings.Cut(kv, "=")
	if !ok || name == "" {
		return fmt.Errorf("refusing to run orama: environment entry %q is not KEY=VALUE", kv)
	}
	if slices.Contains(reservedEnv, name) || slices.Contains(secrets.SecretEnvNames, name) ||
		strings.HasPrefix(name, "XDG_") || strings.HasPrefix(name, "INFISICAL_") {
		return fmt.Errorf("refusing to run orama: RunOpts.Env may not set %s", name)
	}
	return nil
}

// checkDir requires dir to exist inside Home or WorkDir after resolving
// symlinks on both sides.
func (r *Runner) checkDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("refusing to run orama: working directory %q must be absolute", dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("refusing to run orama: working directory %s: %w", dir, err)
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return fmt.Errorf("refusing to run orama: working directory %s is not a directory", dir)
	}
	for _, root := range []string{r.Home, r.WorkDir} {
		if root == "" {
			continue
		}
		base, err := filepath.EvalSymlinks(root)
		if err != nil {
			return fmt.Errorf("refusing to run orama: %s: %w", root, err)
		}
		if rel, err := filepath.Rel(base, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("refusing to run orama in %s: a working directory must be inside the isolated HOME %s or the run's work dir %q", dir, r.Home, r.WorkDir)
}
