package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Paths and names on a stagenet node. They are the ones deploy.sh installs; nothing here is
// discovered from the node.
const (
	netnsName    = "orama-global"
	nodeHelper   = "/usr/local/lib/orama-stagenet/stagenet-node"
	oramadBinary = "/usr/lib/orama-global/bin/oramad"
	chainHome    = "/var/lib/orama-global/chain"
	agentFwdSock = "/run/orama-stagenet-fwd/agent.sock"
	agentSock    = "/run/orama-stagenet/agent.sock"
	// namespaceAddr is where the chain's RPC and REST API listen on a co-located machine
	// (core/pkg/constants, GlobalNetnsAddr); the node's host reaches it over the veth pair.
	namespaceAddr = "198.18.0.2"
	// remoteRPCPort and remoteRESTPort are oramad's RPC and REST listeners on that address.
	remoteRPCPort  = 31001
	remoteRESTPort = 31003
	sshConnectWait = 15 * time.Second
	agentReadyWait = 45 * time.Second
	// agentTTL bounds the smoke run's agent on the node, whatever happens to the ssh session.
	agentTTL = "3h"
)

// shellQuote quotes s for a POSIX shell: single quotes, with each embedded quote closed, escaped and
// reopened. Everything sent to a remote shell goes through it, so a value that contains shell
// syntax is passed through as text.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// sshRunner runs commands on the stagenet nodes through the operator's own ssh configuration (the
// aliases in ~/.ssh/config carry the user, host and key). It shares one connection per host.
type sshRunner struct {
	ctlDir string
}

func newSSHRunner() (*sshRunner, error) {
	dir, err := os.MkdirTemp("/tmp", "stagenet-ssh")
	if err != nil {
		return nil, fmt.Errorf("create the ssh control directory: %w", err)
	}
	return &sshRunner{ctlDir: dir}, nil
}

func (s *sshRunner) close() { _ = os.RemoveAll(s.ctlDir) }

func (s *sshRunner) baseArgs(alias string) []string {
	return []string{
		"-o", "BatchMode=yes", "-o", fmt.Sprintf("ConnectTimeout=%d", int(sshConnectWait.Seconds())),
		"-o", "ServerAliveInterval=15",
		"-o", "ControlMaster=auto", "-o", "ControlPersist=120",
		"-o", "ControlPath=" + filepath.Join(s.ctlDir, "%C"),
		alias,
	}
}

// output runs one remote command string and returns its stdout. On failure the error carries the
// command's stderr, which never holds key material: nothing this program runs prints any.
func (s *sshRunner) output(ctx context.Context, alias, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", append(s.baseArgs(alias), command)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("ssh %s %q: %w: %s", alias, abbreviate(command), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func abbreviate(s string) string {
	const limit = 80
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// tunnel forwards a free local loopback port to the chain's listener on the co-located namespace
// address (198.18.0.2), which the node's host reaches over the veth pair: `ssh -L` connects from the
// host's own namespace. It returns the local address. The forward ends with ctx.
func (s *sshRunner) tunnel(ctx context.Context, alias string, remotePort int) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("pick a local port for the %s tunnel: %w", alias, err)
	}
	local := ln.Addr().String()
	ln.Close()
	target := net.JoinHostPort(namespaceAddr, fmt.Sprint(remotePort))
	fwd := exec.CommandContext(ctx, "ssh", append([]string{"-N", "-o", "ExitOnForwardFailure=yes", "-L", local + ":" + target}, s.baseArgs(alias)...)...)
	var stderr bytes.Buffer
	fwd.Stderr = &stderr
	if err := fwd.Start(); err != nil {
		return "", fmt.Errorf("start the %s tunnel: %w", alias, err)
	}
	go func() { _ = fwd.Wait() }()
	deadline := time.Now().Add(sshConnectWait)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", local, time.Second); err == nil {
			c.Close()
			return local, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("the tunnel to %s %s did not come up: %s", alias, target, strings.TrimSpace(stderr.String()))
}

// agentCommand is the remote command that exports the operator key from oramad's test keyring
// straight into the signing agent on the same host. The key never leaves the node: it crosses one
// pipe. The agent listens on a root-owned socket (for `orama` run on the node) and on one owned by
// the ssh login user (for the forward this program makes).
func agentCommand() string {
	export := shellJoin("runuser", "-u", "orama-chain", "--", oramadBinary, "keys", "export", "validator",
		"--unarmored-hex", "--unsafe", "-y", "--keyring-backend", "test", "--home", chainHome)
	agent := nodeHelper + " agent --ttl " + agentTTL + " --listen " + agentSock + ":0 --listen " + agentFwdSock + `:"$SUDO_UID"`
	return "sudo sh -c " + shellQuote(export+" 2>&1 | "+agent)
}

// agentSession is a running signing agent on one node, forwarded to a local socket.
type agentSession struct {
	socket string
	stop   func()
}

// startAgent starts the agent on the node and forwards its login-user socket to localSock. It
// returns once the agent answers through the forward.
func (s *sshRunner) startAgent(ctx context.Context, alias, localSock string) (*agentSession, error) {
	agentCtx, cancel := context.WithCancel(ctx)
	var agentErr bytes.Buffer
	// -tt gives the session a terminal, so ending it hangs the agent up: without one the agent would
	// outlive the ssh client that started it, holding the operator key in memory.
	agent := exec.CommandContext(agentCtx, "ssh", append(append([]string{"-tt"}, s.baseArgs(alias)...), agentCommand())...)
	agent.Stderr = &agentErr
	if err := agent.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start the signing agent on %s: %w", alias, err)
	}
	forward := exec.CommandContext(agentCtx, "ssh", append([]string{
		"-N", "-o", "ExitOnForwardFailure=yes", "-o", "StreamLocalBindUnlink=yes",
		"-L", localSock + ":" + agentFwdSock,
	}, s.baseArgs(alias)...)...)
	// Wait for the agent's socket before forwarding to it: ssh -L to a missing remote socket only
	// fails when a client connects.
	stop := func() {
		cancel()
		_ = agent.Wait()
		if forward.Process != nil {
			_ = forward.Wait()
		}
		_ = os.Remove(localSock)
		// The pty hang-up ends the agent, and its --ttl bounds it if that ever fails.
	}
	if err := s.waitRemoteSocket(agentCtx, alias); err != nil {
		stop()
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(agentErr.String()))
	}
	if err := forward.Start(); err != nil {
		stop()
		return nil, fmt.Errorf("forward the signing agent socket of %s: %w", alias, err)
	}
	sess := &agentSession{socket: localSock, stop: stop}
	if err := waitLocalSocket(agentCtx, localSock); err != nil {
		stop()
		return nil, err
	}
	return sess, nil
}

// agentProbeCommand asks the agent on the node for its account through the forwarded socket. It is
// the readiness check, not the socket file's existence: a socket file left by an earlier run's agent
// exists before the new agent has replaced it, and a request through it fails.
func agentProbeCommand() string {
	return shellJoin("curl", "-sf", "--max-time", "2", "--unix-socket", agentFwdSock, "http://agent"+agentAccountPath)
}

func (s *sshRunner) waitRemoteSocket(ctx context.Context, alias string) error {
	deadline := time.Now().Add(agentReadyWait)
	for time.Now().Before(deadline) {
		if _, err := s.output(ctx, alias, agentProbeCommand()); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("the signing agent's socket did not appear on the node (is stagenet-node installed and oramad's keyring readable?)")
}

func waitLocalSocket(ctx context.Context, path string) error {
	deadline := time.Now().Add(agentReadyWait)
	for time.Now().Before(deadline) {
		if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("the forwarded agent socket %s did not appear", path)
}
