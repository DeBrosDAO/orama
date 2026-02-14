package install

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

const sourceArchivePath = "/tmp/network-source.tar.gz"

// resolveSSHCredentials finds SSH credentials for the given VPS IP.
// First checks remote-nodes.conf, then prompts interactively.
func resolveSSHCredentials(vpsIP string) (inspector.Node, error) {
	confPath := findRemoteNodesConf()
	if confPath != "" {
		nodes, err := inspector.LoadNodes(confPath)
		if err == nil {
			for _, n := range nodes {
				if n.Host == vpsIP {
					// Expand ~ in SSH key path
					if n.SSHKey != "" && strings.HasPrefix(n.SSHKey, "~") {
						home, _ := os.UserHomeDir()
						n.SSHKey = filepath.Join(home, n.SSHKey[1:])
					}
					return n, nil
				}
			}
		}
	}

	// Not found in config — prompt interactively
	return promptSSHCredentials(vpsIP), nil
}

// findRemoteNodesConf searches for the remote-nodes.conf file.
func findRemoteNodesConf() string {
	candidates := []string{
		"scripts/remote-nodes.conf",
		"../scripts/remote-nodes.conf",
		"network/scripts/remote-nodes.conf",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// promptSSHCredentials asks the user for SSH credentials interactively.
func promptSSHCredentials(vpsIP string) inspector.Node {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("\nSSH credentials for %s\n", vpsIP)
	fmt.Print("  SSH user (default: ubuntu): ")
	user, _ := reader.ReadString('\n')
	user = strings.TrimSpace(user)
	if user == "" {
		user = "ubuntu"
	}

	fmt.Print("  SSH password: ")
	password, _ := reader.ReadString('\n')
	password = strings.TrimSpace(password)

	return inspector.Node{
		User:     user,
		Host:     vpsIP,
		Password: password,
	}
}

// uploadFile copies a local file to a remote host via SCP.
func uploadFile(node inspector.Node, localPath, remotePath string) error {
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
			localPath, dest,
		)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SCP failed: %w", err)
	}
	return nil
}

// runSSHStreaming executes a command on a remote host via SSH,
// streaming stdout/stderr to the local terminal in real-time.
func runSSHStreaming(node inspector.Node, command string) error {
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
			fmt.Sprintf("%s@%s", node.User, node.Host),
			command,
		)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin // Allow password prompts from remote sudo

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSH command failed: %w", err)
	}
	return nil
}

