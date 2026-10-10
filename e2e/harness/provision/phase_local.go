package provision

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"golang.org/x/crypto/ssh"
)

const (
	// dirMode is the mode of every run directory.
	dirMode = 0o700
	// secretMode is the mode of the SSH key and known_hosts.
	secretMode = 0o600
	// agentLogName receives the test agent's output.
	agentLogName = "agent.log"
)

// goPassThrough are Go settings of the operator's environment the builds keep.
var goPassThrough = []string{"GOFLAGS", "GOTOOLCHAIN", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOPROXY", "GOSUMDB"}

// AgentLogPath is the test agent's log in the run's work dir.
func AgentLogPath(workDir string) string { return filepath.Join(workDir, agentLogName) }

func (r *run) makeWorkDir(context.Context) error {
	if _, err := os.Stat(StatePath(r.cfg.WorkDir)); err == nil {
		return fmt.Errorf("%s already holds a run's state: tear it down with Down, or use another %s", r.cfg.WorkDir, EnvWorkDir)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to check for an earlier state in %s: %w", r.cfg.WorkDir, err)
	}
	for _, dir := range []string{
		r.cfg.WorkDir, r.cfg.ArtifactDir, filepath.Join(r.cfg.WorkDir, tmpDirName),
		filepath.Join(r.cfg.WorkDir, binDir), filepath.Join(r.cfg.WorkDir, archiveDir),
	} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}
	// The default work dir has a predictable name in the shared temp dir:
	// one someone else created first, or made a link, is refused.
	if err := secrets.MakePrivateDir(r.cfg.WorkDir); err != nil {
		return fmt.Errorf("refusing the work dir: %w", err)
	}
	return nil
}

// buildBinaries builds the CLI under test, and the previous release's CLI
// when a ref was given.
func (r *run) buildBinaries(ctx context.Context) error {
	env, err := r.resolveGoEnv(ctx)
	if err != nil {
		return err
	}
	r.goEnv = env
	if err := r.goBuild(ctx, filepath.Join(r.cfg.RepoRoot, "core"), r.st.OramaBin); err != nil {
		return err
	}
	ref, ok := strings.CutPrefix(r.cfg.PreviousArchive, previousRefPrefix)
	if !ok {
		return nil
	}
	src := filepath.Join(r.cfg.WorkDir, prevSourceDir)
	if err := r.exportRef(ctx, ref, src); err != nil {
		return err
	}
	prev := filepath.Join(r.cfg.WorkDir, binDir, oramaPrevName)
	if err := r.goBuild(ctx, filepath.Join(src, "core"), prev); err != nil {
		return fmt.Errorf("failed to build the orama CLI of %s: %w", ref, err)
	}
	r.st.PreviousOramaBin = prev
	return nil
}

// hostEnv is the operator's PATH and real HOME plus the Go pass-through
// settings: what local tools need, without any token.
func hostEnv() []string {
	return append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}, goSettings()...)
}

// goSettings are the goPassThrough variables the operator has set.
func goSettings() []string {
	var env []string
	for _, name := range goPassThrough {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// resolveGoEnv reads the toolchain's cache locations, so builds under the
// test agent's HOME reuse the operator's module and build caches.
func (r *run) resolveGoEnv(ctx context.Context) ([]string, error) {
	out, err := r.d.cmd.Run(ctx, command{name: "go", args: []string{"env", "GOPATH", "GOMODCACHE", "GOCACHE"}, env: hostEnv()})
	if err != nil {
		return nil, fmt.Errorf("failed to read the Go environment: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	names := []string{"GOPATH", "GOMODCACHE", "GOCACHE"}
	if len(lines) != len(names) {
		return nil, fmt.Errorf("`go env` printed %d lines, want %d", len(lines), len(names))
	}
	var env []string
	for i, name := range names {
		value := strings.TrimSpace(lines[i])
		if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("`go env %s` is %q, not an absolute path", name, value)
		}
		env = append(env, name+"="+value)
	}
	return append(env, goSettings()...), nil
}

func (r *run) goBuild(ctx context.Context, moduleDir, out string) error {
	env := append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}, r.goEnv...)
	_, err := r.runLogged(ctx, command{name: "go", args: []string{"build", "-o", out, "./cmd/orama"}, dir: moduleDir, env: env})
	if err != nil {
		return fmt.Errorf("failed to build %s: %w", out, err)
	}
	return nil
}

// exportRef writes the tree of ref into dest with git archive, reading the
// repository only.
func (r *run) exportRef(ctx context.Context, ref, dest string) error {
	tarball := dest + ".tar"
	if _, err := r.runLogged(ctx, command{name: "git", args: []string{"-C", r.cfg.RepoRoot, "archive", "--format=tar", "--output=" + tarball, ref}, env: hostEnv()}); err != nil {
		return fmt.Errorf("failed to export %s: %w", ref, err)
	}
	if err := os.MkdirAll(dest, dirMode); err != nil {
		return fmt.Errorf("failed to create %s: %w", dest, err)
	}
	if _, err := r.runLogged(ctx, command{name: "tar", args: []string{"-xf", tarball, "-C", dest}, env: hostEnv()}); err != nil {
		return fmt.Errorf("failed to unpack %s: %w", ref, err)
	}
	return nil
}

// startTestAgent starts the throwaway wallet with the CLI(s) approved. The
// agent log stays open for the agent's life, which outlasts Up.
func (r *run) startTestAgent(ctx context.Context) error {
	approvals := []agent.Approval{{Binary: r.st.OramaBin, Caps: agentCaps()}}
	if r.st.PreviousOramaBin != "" {
		approvals = append(approvals, agent.Approval{Binary: r.st.PreviousOramaBin, Caps: agentCaps()})
	}
	// The agent writes its log itself, for its whole life, so no redacting
	// writer can sit in between: the log stays in the private work dir, and
	// the runner collects a redacted copy (AgentLogPath).
	logPath := AgentLogPath(r.cfg.WorkDir)
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, logFileMode)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", logPath, err)
	}
	a, err := r.d.startAgent(ctx, agent.StartConfig{RWBin: r.cfg.RWBin, AgentBin: r.cfg.RWAgentBin, Approvals: approvals,
		Log: logFile, Redact: r.red.Add})
	if err != nil {
		return errors.Join(fmt.Errorf("failed to start the test RootWallet agent: %w", err), logFile.Close())
	}
	r.agent, r.agentLog = a, logFile
	r.st.Home, r.st.RWSock, r.st.OperatorAddress = a.Dir, a.Sock, a.Address
	sshDir := filepath.Join(a.Dir, sshDirName)
	r.st.SSHKeyFile, r.st.KnownHostsFile = filepath.Join(sshDir, sshKeyName), filepath.Join(sshDir, knownHostsName)
	r.log.Infof("test wallet %s, agent socket %s", a.Address, a.Sock)
	return nil
}

// buildArchives builds and signs HEAD, and the previous release when asked.
func (r *run) buildArchives(ctx context.Context) error {
	head := filepath.Join(r.cfg.WorkDir, archiveDir, headArchive)
	coreDir := filepath.Join(r.cfg.RepoRoot, "core")
	if _, err := r.runLogged(ctx, command{name: r.st.OramaBin, args: []string{"maint", "build", "--output", head}, dir: coreDir, env: r.cliEnv()}); err != nil {
		return fmt.Errorf("failed to build the HEAD archive: %w", err)
	}
	r.st.ArchivePath = head
	ref, isRef := strings.CutPrefix(r.cfg.PreviousArchive, previousRefPrefix)
	switch {
	case isRef:
		prev := filepath.Join(r.cfg.WorkDir, archiveDir, prevArchive)
		dir := filepath.Join(r.cfg.WorkDir, prevSourceDir, "core")
		// The previous release's own CLI builds it, at the path that release has:
		// top-level `build`, before `orama maint` existed.
		if _, err := r.runLogged(ctx, command{name: r.st.PreviousOramaBin, args: []string{"build", "--output", prev}, dir: dir, env: r.cliEnv()}); err != nil {
			return fmt.Errorf("failed to build the archive of %s: %w", ref, err)
		}
		r.st.PreviousArchivePath = prev
	case r.cfg.PreviousArchive != "":
		if err := r.d.verifyArchive(r.cfg.PreviousArchive, r.st.OperatorAddress); err != nil {
			return fmt.Errorf("%s %s cannot be installed on this run (its only trusted signer is the test wallet %s): %w; "+
				"pass %s=%s<git ref> to build the previous release with the test wallet",
				EnvPreviousArchive, r.cfg.PreviousArchive, r.st.OperatorAddress, err, EnvPreviousArchive, previousRefPrefix)
		}
		r.st.PreviousArchivePath = r.cfg.PreviousArchive
	}
	return nil
}

// makeSSHKey writes the run's ed25519 key and an empty known_hosts inside the
// agent directory, which Down shreds.
func (r *run) makeSSHKey(context.Context) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate the run's SSH key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "e2e-"+r.cfg.RunID)
	if err != nil {
		return fmt.Errorf("failed to encode the run's SSH key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return fmt.Errorf("failed to encode the run's SSH public key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.st.SSHKeyFile), dirMode); err != nil {
		return fmt.Errorf("failed to create the SSH key directory: %w", err)
	}
	if err := writeNewFile(r.st.SSHKeyFile, pem.EncodeToMemory(block)); err != nil {
		return fmt.Errorf("failed to write the run's SSH key: %w", err)
	}
	if err := writeNewFile(r.st.KnownHostsFile, nil); err != nil {
		return fmt.Errorf("failed to write the run's known_hosts: %w", err)
	}
	r.pubKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	return nil
}
