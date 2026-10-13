// Package agent runs the throwaway RootWallet agent an e2e run signs with: a
// random wallet in a short, private directory, served by rw-agent-headless
// with only the named binaries pre-approved, and removed without a trace when
// the run ends. It never touches the operator's ~/.rootwallet.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Capabilities rw-agent-headless can pre-approve for a binary.
const (
	CapVaultSSH          = "vault:ssh"
	CapVaultPassword     = "vault:password"
	CapWalletAddress     = "wallet:address"
	CapWalletSign        = "wallet:sign"
	CapWalletSignArchive = "wallet:sign:orama-archive"
	// CapWalletSignOramaTx cannot be pre-approved on a headless agent yet:
	// RootWalletHeadlessTask delivers it.
	CapWalletSignOramaTx = "wallet:sign:orama-tx"
)

// RootWalletHeadlessTask is the RootWallet work that lets rw-agent-headless
// pre-approve the archive and ORAMA transaction signing capabilities.
const RootWalletHeadlessTask = "RootWallet task 2857"

// OramaCaps is what the orama CLI asks the agent for during an e2e run.
var OramaCaps = []string{CapVaultSSH, CapVaultPassword, CapWalletAddress, CapWalletSign, CapWalletSignArchive}

const (
	// DefaultBaseDir holds the agent directory; short, because a Unix socket
	// path is limited to about 100 bytes on macOS.
	DefaultBaseDir = "/tmp"
	// DirPrefix names every agent directory; StopDir refuses any other.
	DirPrefix = "e2e-rw-"
	// maxSocketPath is the longest socket path accepted.
	maxSocketPath = 100
	// DefaultReadyTimeout bounds the wait for the ready file.
	DefaultReadyTimeout = 60 * time.Second
	// initTimeout bounds `rw init`.
	initTimeout = 2 * time.Minute
	// stopTimeout is how long the agent has to exit after SIGTERM.
	stopTimeout = 10 * time.Second
	// pollInterval spaces readiness and exit checks.
	pollInterval = 100 * time.Millisecond
	// tailBytes is how much agent output an error quotes.
	tailBytes = 4096
)

// File names inside the agent directory.
const (
	socketName   = "a.sock"
	passwordName = "pw"
	mnemonicName = "mnemonic.txt"
	readyName    = "ready.json"
	agentLogName = "agent.log"
)

// Approval pre-approves Caps for the binary at Binary (an absolute path; the
// agent pins its path and SHA-256 when it starts).
type Approval struct {
	Binary string
	Caps   []string
}

// StartConfig is how to start the agent.
type StartConfig struct {
	// RWBin is the rw CLI (creates the wallet); AgentBin is rw-agent-headless.
	RWBin, AgentBin string
	Approvals       []Approval
	// BaseDir defaults to DefaultBaseDir.
	BaseDir string
	// ReadyTimeout defaults to DefaultReadyTimeout.
	ReadyTimeout time.Duration
	// Redact, when set, is given the wallet password and mnemonic as soon as
	// they are generated, before anything that could print them runs, so
	// the caller's redactor masks them.
	Redact func(values ...string) error `json:"-"`
	// Log, when set, receives the output of rw init and of the agent; the
	// agent writes to it directly, so it stays open for the agent's life and
	// is the caller's to close. Without one the agent writes to agent.log in
	// its own directory.
	Log *os.File
}

// Agent is a running throwaway agent.
type Agent struct {
	// Dir is the agent directory; it is also the HOME the orama CLI runs
	// with, so the wallet is Dir/.rootwallet.
	Dir string
	// Sock is the agent socket; Address is the wallet's EVM address.
	Sock, Address string
	PID           int

	cmd     *exec.Cmd
	exited  chan struct{}
	waitErr error
	// log is the agent's stdout and stderr; logStart is where its output
	// begins in it; ownsLog is set when launch created it.
	log      *os.File
	logStart int64
	ownsLog  bool
	stopOnce sync.Once
	stopErr  error
}

// SockPath is the agent socket of the agent directory dir.
func SockPath(dir string) string { return filepath.Join(dir, socketName) }

// Env is what a process needs to use this agent and nothing else: HOME
// pointed at the agent directory and RW_AGENT_SOCK set explicitly.
func (a *Agent) Env() []string {
	return []string{"HOME=" + a.Dir, "RW_AGENT_SOCK=" + a.Sock}
}

// Start creates a random wallet and serves it with rw-agent-headless.
func Start(ctx context.Context, cfg StartConfig) (*Agent, error) {
	if err := checkConfig(&cfg); err != nil {
		return nil, err
	}
	dir, err := makeDir(cfg.BaseDir)
	if err != nil {
		return nil, err
	}
	a, err := start(ctx, cfg, dir)
	if err != nil {
		if a != nil {
			return nil, errors.Join(err, a.Stop())
		}
		return nil, errors.Join(err, shredTree(dir))
	}
	return a, nil
}

// start creates the wallet and serves it. The password file exists only
// until the agent reports ready: rw-agent-headless reads it once, while it
// prepares and before it binds its socket (rootwallet
// apps/desktop/src-tauri/src/agent_server/headless/mod.rs), so it is shredded
// then instead of staying on disk for the agent's life.
func start(ctx context.Context, cfg StartConfig, dir string) (*Agent, error) {
	password, err := newPassword()
	if err != nil {
		return nil, err
	}
	if err := register(cfg, password); err != nil {
		return nil, err
	}
	pwPath := filepath.Join(dir, passwordName)
	if err := writeSecret(pwPath, password); err != nil {
		return nil, err
	}
	if err := createWallet(ctx, cfg, dir, password); err != nil {
		return nil, err
	}
	a, err := launch(cfg, dir)
	if err != nil {
		return nil, err
	}
	if err := a.waitReady(ctx, cfg.ReadyTimeout); err != nil {
		return a, err
	}
	if err := shredFile(pwPath); err != nil {
		return a, fmt.Errorf("failed to shred the agent's password file once it was ready: %w", err)
	}
	return a, a.checkStatus(ctx)
}

// register hands generated secrets to cfg.Redact.
func register(cfg StartConfig, values ...string) error {
	if cfg.Redact == nil {
		return nil
	}
	if err := cfg.Redact(values...); err != nil {
		return fmt.Errorf("failed to register the test wallet's secrets for redaction: %w", err)
	}
	return nil
}

// checkConfig fills defaults and refuses what cannot work.
func checkConfig(cfg *StartConfig) error {
	if cfg.BaseDir == "" {
		cfg.BaseDir = DefaultBaseDir
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = DefaultReadyTimeout
	}
	for _, bin := range []string{cfg.RWBin, cfg.AgentBin} {
		if err := checkExecutable(bin); err != nil {
			return err
		}
	}
	if len(cfg.Approvals) == 0 {
		return errors.New("the test agent needs at least one approved binary")
	}
	for _, ap := range cfg.Approvals {
		if !filepath.IsAbs(ap.Binary) || len(ap.Caps) == 0 {
			return fmt.Errorf("approval %q: the binary must be an absolute path with at least one capability", ap.Binary)
		}
		if err := checkExecutable(ap.Binary); err != nil {
			return err
		}
	}
	return nil
}

func checkExecutable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary %q: %w", path, err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("binary %s is not an executable file", path)
	}
	return nil
}

// makeDir creates the private agent directory and checks it is isolated and
// short enough for the socket.
func makeDir(base string) (string, error) {
	dir, err := os.MkdirTemp(base, DirPrefix)
	if err != nil {
		return "", fmt.Errorf("failed to create the agent directory under %s: %w", base, err)
	}
	dir = resolve(dir)
	if err := checkIsolation(dir); err != nil {
		return "", errors.Join(err, os.RemoveAll(dir))
	}
	if sock := filepath.Join(dir, socketName); len(sock) > maxSocketPath {
		return "", errors.Join(fmt.Errorf("agent socket path %s is longer than %d bytes: use a shorter base directory", sock, maxSocketPath),
			os.RemoveAll(dir))
	}
	return dir, nil
}

// createWallet runs `rw init` with a random mnemonic, then shreds it. The
// password goes in ROOTWALLET_PASSWORD: `rw init` reads a new password only
// from that variable or a terminal prompt, never from a pipe (rootwallet
// apps/cli/src/lib/password.ts askNewPassword); the variable lives in that
// one short-lived process's environment.
func createWallet(ctx context.Context, cfg StartConfig, dir, password string) error {
	mnemonic, err := newMnemonic()
	if err != nil {
		return err
	}
	if err := register(cfg, mnemonic); err != nil {
		return err
	}
	mnemonicPath := filepath.Join(dir, mnemonicName)
	if err := writeSecret(mnemonicPath, mnemonic+"\n"); err != nil {
		return err
	}
	ictx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	out := &tail{limit: tailBytes}
	cmd := exec.CommandContext(ictx, cfg.RWBin, "init", "--mnemonic-file", mnemonicPath)
	cmd.Env = append(baseEnv(dir), "ROOTWALLET_PASSWORD="+password)
	cmd.Stdout, cmd.Stderr = out, withLog(out, cfg.Log)
	runErr := cmd.Run()
	shredErr := shredFile(mnemonicPath)
	if runErr != nil {
		return errors.Join(fmt.Errorf("rw init failed: %w: %s", runErr, strings.TrimSpace(out.String())), shredErr)
	}
	return shredErr
}

// baseEnv is the environment rw and the agent run with: PATH, and HOME set
// to the agent directory. XDG_DATA_HOME and everything else are left out.
func baseEnv(dir string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
}

func withLog(w io.Writer, log *os.File) io.Writer {
	if log == nil {
		return w
	}
	return io.MultiWriter(w, log)
}
