package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	// remoteRPCPort and remoteRESTPort are oramad's loopback listeners inside the namespace.
	remoteRPCPort  = 31001
	remoteRESTPort = 31003
	sshConnectWait = 15 * time.Second
	agentReadyWait = 45 * time.Second
	// agentTTL bounds the smoke run's agent on the node, whatever happens to the ssh session.
	agentTTL = "3h"
)

// bridgeScript pumps its stdin and stdout to a TCP port on the loopback it runs on. Run inside the
// orama-global namespace it is how this program reaches oramad's namespace-local listeners over
// ssh: `ssh -L` cannot, because sshd connects from the root namespace.
const bridgeScript = `import os, socket, sys, threading
s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=10)
s.settimeout(None)
def up():
    try:
        while True:
            d = os.read(0, 65536)
            if not d:
                break
            s.sendall(d)
    except OSError:
        pass
    try:
        s.shutdown(socket.SHUT_WR)
    except OSError:
        pass
threading.Thread(target=up, daemon=True).start()
try:
    while True:
        d = s.recv(65536)
        if not d:
            break
        os.write(1, d)
except OSError:
    pass
`

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

// inNetns is the command that runs args as root inside the orama-global namespace.
func inNetns(args ...string) string {
	return "sudo ip netns exec " + netnsName + " " + shellJoin(args...)
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

// tunnel listens on a loopback port and bridges every connection to the node's namespace-local
// loopback port. It returns the local address. The listener closes with ctx.
func (s *sshRunner) tunnel(ctx context.Context, alias string, remotePort int) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen for the %s tunnel: %w", alias, err)
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	remote := inNetns("python3", "-c", bridgeScript, fmt.Sprint(remotePort))
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				wg.Wait()
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.pipe(ctx, conn, alias, remote)
			}()
		}
	}()
	return ln.Addr().String(), nil
}

// pipe connects one local connection to one remote bridge process.
func (s *sshRunner) pipe(ctx context.Context, conn net.Conn, alias, remote string) {
	defer conn.Close()
	cmd := exec.CommandContext(ctx, "ssh", append(s.baseArgs(alias), remote)...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(in, conn)
		in.Close()
	}()
	go func() {
		_, _ = io.Copy(conn, out)
		close(done)
	}()
	<-done
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
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

func (s *sshRunner) waitRemoteSocket(ctx context.Context, alias string) error {
	deadline := time.Now().Add(agentReadyWait)
	for time.Now().Before(deadline) {
		if _, err := s.output(ctx, alias, "test -S "+agentFwdSock); err == nil {
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
