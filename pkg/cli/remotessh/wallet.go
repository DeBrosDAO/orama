package remotessh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// PrepareNodeKeys resolves wallet-derived SSH keys for all nodes.
// Calls `rw vault ssh get <host>/<user> --priv` for each unique host/user,
// writes PEMs to temp files, and sets node.SSHKey for each node.
//
// The nodes slice is modified in place — each node.SSHKey is set to
// the path of the temporary key file.
//
// Returns a cleanup function that zero-overwrites and removes all temp files.
// Caller must defer cleanup().
func PrepareNodeKeys(nodes []inspector.Node) (cleanup func(), err error) {
	rw, err := rwBinary()
	if err != nil {
		return nil, err
	}

	// Create temp dir for all keys
	tmpDir, err := os.MkdirTemp("", "orama-ssh-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// Track resolved keys by host/user to avoid duplicate rw calls
	keyPaths := make(map[string]string) // "host/user" → temp file path
	var allKeyPaths []string

	for i := range nodes {
		// Use VaultTarget if set, otherwise default to Host/User
		var key string
		if nodes[i].VaultTarget != "" {
			key = nodes[i].VaultTarget
		} else {
			key = nodes[i].Host + "/" + nodes[i].User
		}
		if existing, ok := keyPaths[key]; ok {
			nodes[i].SSHKey = existing
			continue
		}

		// Call rw to get the private key PEM
		host, user := parseVaultTarget(key)
		pem, err := resolveWalletKey(rw, host, user)
		if err != nil {
			// Cleanup any keys already written before returning error
			cleanupKeys(tmpDir, allKeyPaths)
			return nil, fmt.Errorf("resolve key for %s: %w", nodes[i].Name(), err)
		}

		// Write PEM to temp file with restrictive perms
		keyFile := filepath.Join(tmpDir, fmt.Sprintf("id_%d", i))
		if err := os.WriteFile(keyFile, []byte(pem), 0600); err != nil {
			cleanupKeys(tmpDir, allKeyPaths)
			return nil, fmt.Errorf("write key for %s: %w", nodes[i].Name(), err)
		}

		keyPaths[key] = keyFile
		allKeyPaths = append(allKeyPaths, keyFile)
		nodes[i].SSHKey = keyFile
	}

	cleanup = func() {
		cleanupKeys(tmpDir, allKeyPaths)
	}
	return cleanup, nil
}

// LoadAgentKeys loads SSH keys for the given nodes into the system ssh-agent.
// Used by push fanout to enable agent forwarding.
// Calls `rw vault ssh agent-load <host1/user1> <host2/user2> ...`
func LoadAgentKeys(nodes []inspector.Node) error {
	rw, err := rwBinary()
	if err != nil {
		return err
	}

	// Deduplicate host/user pairs
	seen := make(map[string]bool)
	var targets []string
	for _, n := range nodes {
		var key string
		if n.VaultTarget != "" {
			key = n.VaultTarget
		} else {
			key = n.Host + "/" + n.User
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, key)
	}

	if len(targets) == 0 {
		return nil
	}

	args := append([]string{"vault", "ssh", "agent-load"}, targets...)
	cmd := exec.Command(rw, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr // info messages go to stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rw vault ssh agent-load failed: %w", err)
	}
	return nil
}

// EnsureVaultEntry creates a wallet SSH entry if it doesn't already exist.
// Checks existence via `rw vault ssh get <target> --pub`, and if missing,
// runs `rw vault ssh add <target>` to create it.
func EnsureVaultEntry(vaultTarget string) error {
	rw, err := rwBinary()
	if err != nil {
		return err
	}

	// Check if entry exists by trying to get the public key
	cmd := exec.Command(rw, "vault", "ssh", "get", vaultTarget, "--pub")
	if err := cmd.Run(); err == nil {
		return nil // entry already exists
	}

	// Entry doesn't exist — try to create it
	addCmd := exec.Command(rw, "vault", "ssh", "add", vaultTarget)
	addCmd.Stdin = os.Stdin
	addCmd.Stdout = os.Stderr
	addCmd.Stderr = os.Stderr
	if err := addCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "not unlocked") || strings.Contains(stderr, "session") {
				return fmt.Errorf("wallet is locked — run: rw unlock")
			}
		}
		return fmt.Errorf("rw vault ssh add %s failed: %w", vaultTarget, err)
	}
	return nil
}

// ResolveVaultPublicKey returns the OpenSSH public key string for a vault entry.
// Calls `rw vault ssh get <target> --pub`.
func ResolveVaultPublicKey(vaultTarget string) (string, error) {
	rw, err := rwBinary()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(rw, "vault", "ssh", "get", vaultTarget, "--pub")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "No SSH entry") {
				return "", fmt.Errorf("no vault SSH entry for %s — run: rw vault ssh add %s", vaultTarget, vaultTarget)
			}
			if strings.Contains(stderr, "not unlocked") || strings.Contains(stderr, "session") {
				return "", fmt.Errorf("wallet is locked — run: rw unlock")
			}
			return "", fmt.Errorf("%s", stderr)
		}
		return "", fmt.Errorf("rw command failed: %w", err)
	}

	pubKey := strings.TrimSpace(string(out))
	if !strings.HasPrefix(pubKey, "ssh-") {
		return "", fmt.Errorf("rw returned invalid public key for %s", vaultTarget)
	}
	return pubKey, nil
}

// parseVaultTarget splits a "host/user" vault target string into host and user.
func parseVaultTarget(target string) (host, user string) {
	idx := strings.Index(target, "/")
	if idx < 0 {
		return target, ""
	}
	return target[:idx], target[idx+1:]
}

// resolveWalletKey calls `rw vault ssh get <host>/<user> --priv`
// and returns the PEM string. Requires an active rw session.
func resolveWalletKey(rw string, host, user string) (string, error) {
	target := host + "/" + user
	cmd := exec.Command(rw, "vault", "ssh", "get", target, "--priv")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "No SSH entry") {
				return "", fmt.Errorf("no vault SSH entry for %s — run: rw vault ssh add %s", target, target)
			}
			if strings.Contains(stderr, "not unlocked") || strings.Contains(stderr, "session") {
				return "", fmt.Errorf("wallet is locked — run: rw unlock")
			}
			return "", fmt.Errorf("%s", stderr)
		}
		return "", fmt.Errorf("rw command failed: %w", err)
	}
	pem := string(out)
	if !strings.Contains(pem, "BEGIN OPENSSH PRIVATE KEY") {
		return "", fmt.Errorf("rw returned invalid key for %s", target)
	}
	return pem, nil
}

// rwBinary returns the path to the `rw` binary.
// Checks RW_PATH env var first, then PATH.
func rwBinary() (string, error) {
	if p := os.Getenv("RW_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("RW_PATH=%q not found", p)
	}

	p, err := exec.LookPath("rw")
	if err != nil {
		return "", fmt.Errorf("rw not found in PATH — install rootwallet CLI: https://github.com/DeBrosOfficial/rootwallet")
	}
	return p, nil
}

// cleanupKeys zero-overwrites and removes all key files, then removes the temp dir.
func cleanupKeys(tmpDir string, keyPaths []string) {
	zeros := make([]byte, 512)
	for _, p := range keyPaths {
		_ = os.WriteFile(p, zeros, 0600) // zero-overwrite
		_ = os.Remove(p)
	}
	_ = os.Remove(tmpDir)
}
