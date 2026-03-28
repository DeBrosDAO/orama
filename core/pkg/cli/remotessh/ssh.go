package remotessh

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// SSHOption configures SSH command behavior.
type SSHOption func(*sshOptions)

type sshOptions struct {
	agentForward   bool
	noHostKeyCheck bool
}

// WithAgentForward enables SSH agent forwarding (-A flag).
// Used by push fanout so the hub can reach targets via the forwarded agent.
func WithAgentForward() SSHOption {
	return func(o *sshOptions) { o.agentForward = true }
}

// WithNoHostKeyCheck disables host key verification and uses /dev/null as known_hosts.
// Use for ephemeral servers (sandbox) where IPs are frequently recycled.
func WithNoHostKeyCheck() SSHOption {
	return func(o *sshOptions) { o.noHostKeyCheck = true }
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

	args := []string{"-o", "ConnectTimeout=10", "-o", "IdentitiesOnly=yes", "-i", node.SSHKey}
	if cfg.noHostKeyCheck {
		args = append([]string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}, args...)
	} else {
		args = append([]string{"-o", "StrictHostKeyChecking=accept-new"}, args...)
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

	args := []string{"-o", "ConnectTimeout=10", "-o", "IdentitiesOnly=yes", "-i", node.SSHKey}
	if cfg.noHostKeyCheck {
		args = append([]string{"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}, args...)
	} else {
		args = append([]string{"-o", "StrictHostKeyChecking=accept-new"}, args...)
	}
	if cfg.agentForward {
		args = append(args, "-A")
	}
	args = append(args, fmt.Sprintf("%s@%s", node.User, node.Host), command)

	cmd := exec.Command("ssh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH to %s failed: %w", node.Host, err)
	}
	return nil
}

// SudoPrefix returns "sudo " for non-root users, empty for root.
func SudoPrefix(node inspector.Node) string {
	if node.User == "root" {
		return ""
	}
	return "sudo "
}
