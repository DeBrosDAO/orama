package sandbox

import (
	"fmt"
	"os"
	"os/exec"
)

// SSHInto opens an interactive SSH session to a sandbox node.
func SSHInto(name string, nodeNum int) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	state, err := resolveSandbox(name)
	if err != nil {
		return err
	}

	if nodeNum < 1 || nodeNum > len(state.Servers) {
		return fmt.Errorf("node number must be between 1 and %d", len(state.Servers))
	}

	srv := state.Servers[nodeNum-1]

	sshKeyPath, cleanup, err := resolveVaultKeyOnce(cfg.SSHKey.VaultTarget)
	if err != nil {
		return fmt.Errorf("prepare SSH key: %w", err)
	}

	fmt.Printf("Connecting to %s (%s, %s)...\n", srv.Name, srv.IP, srv.Role)

	// Find ssh binary
	sshBin, err := findSSHBinary()
	if err != nil {
		cleanup()
		return err
	}

	// Run SSH as a child process so cleanup runs after the session ends
	cmd := exec.Command(sshBin,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-i", sshKeyPath,
		fmt.Sprintf("root@%s", srv.IP),
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	cleanup()
	return err
}

// findSSHBinary locates the ssh binary in PATH.
func findSSHBinary() (string, error) {
	paths := []string{"/usr/bin/ssh", "/usr/local/bin/ssh", "/opt/homebrew/bin/ssh"}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ssh binary not found")
}
