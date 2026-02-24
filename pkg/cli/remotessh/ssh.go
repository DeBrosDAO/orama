package remotessh

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// UploadFile copies a local file to a remote host via SCP.
func UploadFile(node inspector.Node, localPath, remotePath string) error {
	dest := fmt.Sprintf("%s@%s:%s", node.User, node.Host, remotePath)

	var cmd *exec.Cmd
	if node.SSHKey != "" {
		cmd = exec.Command("scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			"-i", node.SSHKey,
			localPath, dest,
		)
	} else {
		if _, err := exec.LookPath("sshpass"); err != nil {
			return fmt.Errorf("sshpass not found — install it: brew install hudochenkov/sshpass/sshpass")
		}
		cmd = exec.Command("sshpass", "-p", node.Password,
			"scp",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			"-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no",
			localPath, dest,
		)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SCP to %s failed: %w", node.Host, err)
	}
	return nil
}

// RunSSHStreaming executes a command on a remote host via SSH,
// streaming stdout/stderr to the local terminal in real-time.
func RunSSHStreaming(node inspector.Node, command string) error {
	var cmd *exec.Cmd
	if node.SSHKey != "" {
		cmd = exec.Command("ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			"-i", node.SSHKey,
			fmt.Sprintf("%s@%s", node.User, node.Host),
			command,
		)
	} else {
		cmd = exec.Command("sshpass", "-p", node.Password,
			"ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			"-o", "PreferredAuthentications=password",
			"-o", "PubkeyAuthentication=no",
			fmt.Sprintf("%s@%s", node.User, node.Host),
			command,
		)
	}

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
