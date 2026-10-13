package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// The broker child: `e2e-fleet broker-serve`.
const (
	cmdBrokerServeName = "broker-serve"
	// brokerSecretsFD is the child's fd of the credentials pipe: the first
	// of exec.Cmd.ExtraFiles.
	brokerSecretsFD = 3
	// brokerReadyPrefix starts the one line the child prints on stdout.
	brokerReadyPrefix = "ready "
	// brokerStartBudget bounds the child's start, up to its ready line.
	brokerStartBudget = time.Minute
	// brokerStopBudget bounds its stop after its stdin closes: operations
	// still running are cancelled by the broker's Close.
	brokerStopBudget = 2 * time.Minute
	// featureHomeName is the HOME of feature processes, in the work dir.
	featureHomeName = "feature-home"
)

// brokerSecretNames are the credentials the broker child receives.
var brokerSecretNames = []string{config.EnvHCloudToken, config.EnvCFToken}

// brokerChild is a running `e2e-fleet broker-serve`.
type brokerChild struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	// log is the child's stderr (and any stray stdout), redacted with the
	// run's secrets and token registry.
	log  *secrets.RedactingWriter
	path string
	done chan error
}

// startBrokerChild starts the broker child for the run whose state is at
// statePath and waits for it to serve. Its environment is this process's
// without any secret; the credentials go over a pipe.
func startBrokerChild(ctx context.Context, statePath string) (*brokerChild, error) {
	return startBrokerChildArgs(ctx, statePath, []string{cmdBrokerServeName})
}

// startBrokerChildArgs is startBrokerChild with the child's arguments.
func startBrokerChildArgs(ctx context.Context, statePath string, args []string) (*brokerChild, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to find the runner's executable for the broker child: %w", err)
	}
	log := secrets.NewRedactingWriter(os.Stderr, runRedact(statePath))
	b, secretsW, stdout, err := spawnBroker(exe, args, brokerChildEnv(os.Environ(), statePath), log)
	if err != nil {
		return nil, err
	}
	lines := make(chan string, 1)
	go b.watch(stdout, lines)
	pass := map[string]string{}
	for _, name := range brokerSecretNames {
		if v, ok := secrets.LookupEnv(name); ok {
			pass[name] = v
		}
	}
	if err := errors.Join(secrets.WriteSealed(secretsW, pass), secretsW.Close()); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to pass the credentials to the broker child: %w", err), b.stop())
	}
	if b.path, err = b.ready(ctx, lines); err != nil {
		return nil, errors.Join(err, b.stop())
	}
	return b, nil
}

// spawnBroker starts exe broker-serve in a process group of its own, with
// the read end of a fresh pipe as fd 3; it returns the write end.
func spawnBroker(exe string, args, env []string, log *secrets.RedactingWriter) (*brokerChild, *os.File, io.Reader, error) {
	secretsR, secretsW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create the broker credentials pipe: %w", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env, cmd.Stderr, cmd.ExtraFiles = env, log, []*os.File{secretsR}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("failed to create the broker stop pipe: %w", err), secretsR.Close(), secretsW.Close())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("failed to create the broker ready pipe: %w", err), secretsR.Close(), secretsW.Close())
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("failed to start the broker child: %w", err), secretsR.Close(), secretsW.Close())
	}
	if err := secretsR.Close(); err != nil {
		return nil, nil, nil, errors.Join(fmt.Errorf("failed to close the child's end of the credentials pipe: %w", err), secretsW.Close())
	}
	return &brokerChild{cmd: cmd, stdin: stdin, log: log, done: make(chan error, 1)}, secretsW, stdout, nil
}

// brokerChildEnv is environ without any secret, with E2E_FLEET_STATE set to
// statePath and without the variables that would make the child a client.
func brokerChildEnv(environ []string, statePath string) []string {
	clean, _ := secrets.SplitSecretEnv(environ)
	drop := map[string]bool{config.EnvState: true, broker.EnvSock: true, envSealedFD: true}
	var out []string
	for _, kv := range clean {
		if name, _, _ := strings.Cut(kv, "="); !drop[name] {
			out = append(out, kv)
		}
	}
	return append(out, config.EnvState+"="+statePath)
}

// watch hands the child's first stdout line to lines, copies the rest to
// stderr, and reports the child's exit on b.done.
func (b *brokerChild) watch(stdout io.Reader, lines chan<- string) {
	br := bufio.NewReader(stdout)
	line, _ := br.ReadString('\n')
	lines <- strings.TrimSpace(line)
	_, cerr := io.Copy(b.log, br)
	if cerr != nil {
		cerr = fmt.Errorf("failed to copy the broker child's output: %w", cerr)
	}
	b.done <- errors.Join(cerr, b.cmd.Wait(), b.log.Close())
}

// ready waits for the child's ready line and returns its socket path.
func (b *brokerChild) ready(ctx context.Context, lines <-chan string) (string, error) {
	timer := time.NewTimer(brokerStartBudget)
	defer timer.Stop()
	select {
	case line := <-lines:
		path, ok := strings.CutPrefix(line, brokerReadyPrefix)
		if !ok || !filepath.IsAbs(path) {
			return "", fmt.Errorf("the broker child did not start (first line %q; its errors are above)", line)
		}
		return path, nil
	case <-ctx.Done():
		return "", fmt.Errorf("stopped while the broker child started: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("the broker child was not ready within %s", brokerStartBudget)
	}
}

// stop closes the child's stdin and waits for it; past brokerStopBudget its
// process group is killed.
func (b *brokerChild) stop() error {
	cerr := b.stdin.Close()
	timer := time.NewTimer(brokerStopBudget)
	defer timer.Stop()
	select {
	case err := <-b.done:
		return errors.Join(cerr, wrapBrokerExit(err))
	case <-timer.C:
	}
	kerr := syscall.Kill(-b.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(kerr, syscall.ESRCH) {
		kerr = nil
	}
	return errors.Join(cerr, kerr, fmt.Errorf("the broker child did not stop within %s and was killed", brokerStopBudget), wrapBrokerExit(<-b.done))
}

func wrapBrokerExit(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("the broker child failed: %w", err)
}

// featureHome creates <state>.feature-home beside the state file, empty and
// 0700, the HOME of feature processes: go, git, pnpm and tinygo need one, and
// it must be neither the owner's real home nor the test agent's. It is named
// after the state file, so two runs whose states share a directory never
// empty each other's HOME.
func featureHome(statePath string) (string, error) {
	home := strings.TrimSuffix(statePath, filepath.Ext(statePath)) + "." + featureHomeName
	if err := os.RemoveAll(home); err != nil {
		return "", fmt.Errorf("failed to empty the feature HOME %s: %w", home, err)
	}
	if err := os.Mkdir(home, 0o700); err != nil {
		return "", fmt.Errorf("failed to create the feature HOME %s: %w", home, err)
	}
	return home, nil
}

func newFlagSet(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }

// stderrLogf is a progress logger on stderr (the broker child's stdout
// carries only its ready line).
func stderrLogf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e-fleet: "+format+"\n", args...)
}
