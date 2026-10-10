package remotessh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const (
	// loopbackHost is where a tunnel listens: this machine only.
	loopbackHost = "127.0.0.1"
	// tunnelReadyBudget is how long a tunnel has to accept connections.
	tunnelReadyBudget = 20 * time.Second
	// tunnelPollInterval is how often a starting tunnel is probed.
	tunnelPollInterval = 100 * time.Millisecond
)

// Tunnel is an ssh port forward from a loopback port on this machine to an
// address the node can reach.
type Tunnel struct {
	// Addr is the local host:port that reaches the remote address.
	Addr string

	cmd  *exec.Cmd
	done chan error

	closeOnce sync.Once
	closeErr  error
}

// Close stops the forward. It may be called more than once.
func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		err := <-t.done
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.closeErr = err
		}
	})
	return t.closeErr
}

// tunnelArgs are the ssh arguments of a forward from local port to remote on node.
func tunnelArgs(node inspector.Node, localPort int, remote string) []string {
	args := append([]string{}, node.HostKeyOptions()...)
	args = append(args, baseSSHOptions()...)
	return append(args,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "BatchMode=yes",
		"-N",
		"-i", node.SSHKey,
		"-L", fmt.Sprintf("%s:%d:%s", loopbackHost, localPort, remote),
		fmt.Sprintf("%s@%s", node.User, node.Host),
	)
}

// OpenTunnel forwards a free loopback port on this machine to remote (host:port
// as the node sees it) and returns once it accepts connections. The forward
// runs as the node's ssh user, so the remote address must be one that user may
// reach. Requires node.SSHKey (via PrepareNodeKeys).
func OpenTunnel(ctx context.Context, node inspector.Node, remote string) (*Tunnel, error) {
	if node.SSHKey == "" {
		return nil, fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("ssh", tunnelArgs(node, port, remote)...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the ssh tunnel to %s: %w", node.Host, err)
	}
	t := &Tunnel{Addr: net.JoinHostPort(loopbackHost, strconv.Itoa(port)), cmd: cmd, done: make(chan error, 1)}
	go func() { t.done <- cmd.Wait() }()
	if err := t.waitReady(ctx, node, remote); err != nil {
		return nil, errors.Join(err, t.Close())
	}
	return t, nil
}

// waitReady polls the local end until it accepts a connection, the ssh process
// exits, or the budget runs out.
func (t *Tunnel) waitReady(ctx context.Context, node inspector.Node, remote string) error {
	ctx, cancel := context.WithTimeout(ctx, tunnelReadyBudget)
	defer cancel()
	tick := time.NewTicker(tunnelPollInterval)
	defer tick.Stop()
	for {
		select {
		case err := <-t.done:
			t.done <- err
			return fmt.Errorf("the ssh tunnel to %s (for %s) exited before it was ready: %v", node.Host, remote, err)
		case <-ctx.Done():
			return fmt.Errorf("the ssh tunnel to %s (for %s) was not ready within %s: %w", node.Host, remote, tunnelReadyBudget, ctx.Err())
		case <-tick.C:
			conn, err := net.DialTimeout("tcp", t.Addr, tunnelPollInterval)
			if err == nil {
				return conn.Close()
			}
		}
	}
}

// freeLoopbackPort asks the kernel for an unused loopback port.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		return 0, fmt.Errorf("find a free local port for the ssh tunnel: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
