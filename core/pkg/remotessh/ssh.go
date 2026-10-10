package remotessh

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// SSHOption configures SSH command behavior.
type SSHOption func(*sshOptions)

type sshOptions struct {
	noHostKeyCheck bool
	stdin          io.Reader
}

// WithNoHostKeyCheck disables host key verification and uses /dev/null as known_hosts.
// Use for ephemeral servers (sandbox) where IPs are frequently recycled.
func WithNoHostKeyCheck() SSHOption {
	return func(o *sshOptions) { o.noHostKeyCheck = true }
}

// WithStdin feeds r to the remote command's stdin instead of the local
// terminal's. It is how a secret reaches a remote command without appearing in
// its argv, which every local user on the remote host can read in ps.
func WithStdin(r io.Reader) SSHOption {
	return func(o *sshOptions) { o.stdin = r }
}

// UploadFile copies a local file to a remote host via SCP.
// Requires node.SSHKey to be set (via PrepareNodeKeys).
func UploadFile(node inspector.Node, localPath, remotePath string, opts ...SSHOption) error {
	if node.SSHKey == "" {
		return fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}

	var cfg sshOptions
	for _, o := range opts {
		o(&cfg)
	}

	dest := fmt.Sprintf("%s@%s:%s", node.User, node.Host, remotePath)

	args := append(baseSSHOptions(), "-i", node.SSHKey)
	if cfg.noHostKeyCheck {
		args = append([]string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}, args...)
	} else {
		args = append(node.HostKeyOptions(), args...)
	}
	args = append(args, localPath, dest)

	cmd := exec.Command("scp", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SCP to %s failed: %w", node.Host, err)
	}
	return nil
}

// RunSSHStreaming executes a command on a remote host via SSH,
// streaming stdout/stderr to the local terminal in real-time.
// Requires node.SSHKey to be set (via PrepareNodeKeys).
func RunSSHStreaming(node inspector.Node, command string, opts ...SSHOption) error {
	if node.SSHKey == "" {
		return fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}

	var cfg sshOptions
	for _, o := range opts {
		o(&cfg)
	}

	args := append(baseSSHOptions(), "-i", node.SSHKey)
	if cfg.noHostKeyCheck {
		args = append([]string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}, args...)
	} else {
		args = append(node.HostKeyOptions(), args...)
	}
	args = append(args, fmt.Sprintf("%s@%s", node.User, node.Host), command)

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if cfg.stdin != nil {
		cmd.Stdin = cfg.stdin
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH to %s failed: %w", node.Host, err)
	}
	return nil
}

// Command is the ssh process that runs command on node, with the node's key and
// host-key policy and the same liveness options every other session has. The
// caller sets its stdin, stdout and stderr and runs it: RunSSHStreaming prints to
// the operator's terminal, which a full-screen UI cannot share.
func Command(ctx context.Context, node inspector.Node, command string) (*exec.Cmd, error) {
	if node.SSHKey == "" {
		return nil, fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}
	args := append(node.HostKeyOptions(), baseSSHOptions()...)
	args = append(args, "-i", node.SSHKey, fmt.Sprintf("%s@%s", node.User, node.Host), command)
	return exec.CommandContext(ctx, "ssh", args...), nil
}

// SudoPrefix returns "sudo " for non-root users, empty for root.
func SudoPrefix(node inspector.Node) string {
	if node.User == "root" {
		return ""
	}
	return "sudo "
}

// RunSSHOutput runs a command on the node and returns its stdout.
//
// Distinct from RunSSHStreaming, which relays output to the operator's terminal
// and returns nothing: this is for reading a value back.
func RunSSHOutput(node inspector.Node, command string, opts ...SSHOption) (string, error) {
	res := inspector.RunSSH(context.Background(), node, command)
	if !res.OK() {
		return "", fmt.Errorf("run on %s: %v (stderr: %s)", node.Host, res.Err, res.Stderr)
	}
	return res.Stdout, nil
}

// Liveness of an SSH session to a node. Without them a connection that died
// silently (the node overloaded, a NAT dropping the flow) kept scp and ssh
// waiting forever, and a push or rollout hung on that node with no error.
const (
	sshConnectTimeoutSec   = 10
	sshServerAliveInterval = 15
	sshServerAliveCountMax = 4
)

// baseSSHOptions is every scp and ssh call's connect timeout, identity rule
// and keepalives: a session that answers no keepalive for a minute fails.
func baseSSHOptions() []string {
	return []string{
		"-o", fmt.Sprintf("ConnectTimeout=%d", sshConnectTimeoutSec),
		"-o", fmt.Sprintf("ServerAliveInterval=%d", sshServerAliveInterval),
		"-o", fmt.Sprintf("ServerAliveCountMax=%d", sshServerAliveCountMax),
		"-o", "IdentitiesOnly=yes",
	}
}
