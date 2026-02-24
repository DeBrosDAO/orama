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
	agentForward bool
}

// WithAgentForward enables SSH agent forwarding (-A flag).
// Used by push fanout so the hub can reach targets via the forwarded agent.
func WithAgentForward() SSHOption {
	return func(o *sshOptions) { o.agentForward = true }
}

// UploadFile copies a local file to a remote host via SCP.
// Requires node.SSHKey to be set (via PrepareNodeKeys).
func UploadFile(node inspector.Node, localPath, remotePath string) error {
	if node.SSHKey == "" {
		return fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name())
	}

	dest := fmt.Sprintf("%s@%s:%s", node.User, node.Host, remotePath)

	cmd := exec.Command("scp",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-i", node.SSHKey,
		localPath, dest,
	)

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

	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-i", node.SSHKey,
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
