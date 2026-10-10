package remotessh

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const (
	// tunnelReadyBudget is how long a tunnel has to start accepting connections.
	tunnelReadyBudget = 20 * time.Second
	// tunnelPollInterval is how often the local end is tried.
	tunnelPollInterval = 100 * time.Millisecond
	tunnelLoopback     = "127.0.0.1"
)

// tunnelArgs is the ssh command line that forwards localPort on this machine's
// loopback to remote (host:port, as the node sees it). The node's host-key
// policy and key are the ones every other connection of the node uses;
// ExitOnForwardFailure makes a forward that cannot be set up end the process
// instead of leaving a session with nothing forwarded.
func tunnelArgs(node inspector.Node, localPort int, remote string) []string {
	args := append(node.HostKeyOptions(), baseSSHOptions()...)
	args = append(args,
		"-o", "ExitOnForwardFailure=yes",
		"-i", node.SSHKey, "-N",
		"-L", net.JoinHostPort(tunnelLoopback, strconv.Itoa(localPort))+":"+remote,
		fmt.Sprintf("%s@%s", node.User, node.Host))
	return args
}

// StartTunnel forwards a loopback port of this machine to remote through an SSH
// session to the node, and returns the local address once it accepts
// connections. stop ends the session. It is how a command on the operator's
// laptop reaches a service that only listens inside the node, such as the
// chain's REST API in the orama-global network namespace.
func StartTunnel(ctx context.Context, node inspector.Node, remote string) (local string, stop func(), err error) {
	if node.SSHKey == "" {
		return "", nil, fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}
	port, err := freePort()
	if err != nil {
		return "", nil, fmt.Errorf("find a free local port for the tunnel: %w", err)
	}
	cmd := exec.Command("ssh", tunnelArgs(node, port, remote)...)
	stderr := &lastBytes{max: tunnelStderrKept}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start the SSH tunnel to %s: %w", node.Host, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			<-exited
		})
	}
	local = net.JoinHostPort(tunnelLoopback, strconv.Itoa(port))
	ctx, cancel := context.WithTimeout(ctx, tunnelReadyBudget)
	defer cancel()
	ticker := time.NewTicker(tunnelPollInterval)
	defer ticker.Stop()
	for {
		conn, dialErr := net.DialTimeout("tcp", local, tunnelPollInterval)
		if dialErr == nil {
			_ = conn.Close()
			return local, stop, nil
		}
		select {
		case waitErr := <-exited:
			return "", nil, fmt.Errorf("the SSH tunnel to %s ended before it was ready: %v: %s", node.Host, waitErr, oneLine(stderr.String()))
		case <-ctx.Done():
			stop()
			return "", nil, fmt.Errorf("the SSH tunnel to %s (%s) did not accept connections within %s", node.Host, remote, tunnelReadyBudget)
		case <-ticker.C:
		}
	}
}

// tunnelStderrKept is how much of the tunnel's stderr an error repeats.
const tunnelStderrKept = 1024

// lastBytes keeps the end of what is written to it, at most max bytes: what an
// error shows of a session whose server can print without end.
type lastBytes struct {
	max int
	buf []byte
}

func (l *lastBytes) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	if len(l.buf) > l.max {
		l.buf = append([]byte(nil), l.buf[len(l.buf)-l.max:]...)
	}
	return len(p), nil
}

func (l *lastBytes) String() string { return string(l.buf) }

// oneLine is s on one line with its control characters replaced: it is text a
// server wrote, and it ends up in an error printed on the operator's terminal.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

// freePort asks the kernel for an unused loopback port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(tunnelLoopback, "0"))
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Tunnel is an open StartTunnel for callers that keep it in a value: Addr is the local end, and
// Close ends the session.
type Tunnel struct {
	// Addr is the local host:port that reaches the remote address.
	Addr string
	stop func()
}

// Close ends the tunnel's SSH session. It is safe to call more than once.
func (t *Tunnel) Close() error {
	t.stop()
	return nil
}

// OpenTunnel is StartTunnel returning a Tunnel.
func OpenTunnel(ctx context.Context, node inspector.Node, remote string) (*Tunnel, error) {
	local, stop, err := StartTunnel(ctx, node, remote)
	if err != nil {
		return nil, err
	}
	return &Tunnel{Addr: local, stop: stop}, nil
}
