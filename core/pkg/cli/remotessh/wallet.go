package remotessh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// vaultClient is the interface used by wallet functions to talk to the agent.
// Defaults to the real rwagent.Client; tests replace it with a mock.
type vaultClient interface {
	GetSSHKey(ctx context.Context, host, username, format string) (*rwagent.VaultSSHData, error)
	CreateSSHEntry(ctx context.Context, host, username string) (*rwagent.VaultSSHData, error)
}

// newClient creates the default vaultClient. Package-level var for test injection.
var newClient func() vaultClient = func() vaultClient {
	return rwagent.New(os.Getenv("RW_AGENT_SOCK"))
}

// wrapAgentError wraps rwagent errors with user-friendly messages.
// When the agent is locked, it also triggers the RootWallet desktop app
// to show the unlock dialog via deep link (best-effort, fire-and-forget).
func wrapAgentError(err error, action string) error {
	if rwagent.IsNotRunning(err) {
		return fmt.Errorf("%s: rootwallet agent is not reachable — open the RootWallet desktop app and unlock it", action)
	}
	if rwagent.IsLocked(err) {
		return fmt.Errorf("%s: rootwallet agent is locked — unlock timed out after waiting. Unlock it in the RootWallet desktop app", action)
	}
	if rwagent.IsApprovalDenied(err) {
		return fmt.Errorf("%s: rootwallet access denied — approve this app in the RootWallet desktop app", action)
	}
	return fmt.Errorf("%s: %w", action, err)
}

// PrepareNodeKeys resolves wallet-derived SSH keys for all nodes.
// Retrieves private keys from the rootwallet agent daemon, writes PEMs to
// temp files, and sets node.SSHKey for each node.
//
// The nodes slice is modified in place — each node.SSHKey is set to
// the path of the temporary key file.
//
// Returns a cleanup function that zero-overwrites and removes all temp files.
// Caller must defer cleanup().
func PrepareNodeKeys(nodes []inspector.Node) (cleanup func(), err error) {
	client := newClient()
	ctx := context.Background()

	// Create temp dir for all keys
	tmpDir, err := os.MkdirTemp("", "orama-ssh-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// Track resolved keys by host/user to avoid duplicate agent calls
	keyPaths := make(map[string]string) // "host/user" → temp file path
	var allKeyPaths []string

	for i := range nodes {
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

		host, user := parseVaultTarget(key)
		data, err := client.GetSSHKey(ctx, host, user, "priv")
		if err != nil {
			cleanupKeys(tmpDir, allKeyPaths)
			return nil, wrapAgentError(err, fmt.Sprintf("resolve key for %s", nodes[i].Name()))
		}

		if !strings.Contains(data.PrivateKey, "BEGIN OPENSSH PRIVATE KEY") {
			cleanupKeys(tmpDir, allKeyPaths)
			return nil, fmt.Errorf("agent returned invalid key for %s", nodes[i].Name())
		}

		// Write PEM to temp file with restrictive perms
		keyFile := filepath.Join(tmpDir, fmt.Sprintf("id_%d", i))
		if err := os.WriteFile(keyFile, []byte(data.PrivateKey), 0600); err != nil {
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

// EnsureVaultEntry creates a wallet SSH entry if it doesn't already exist.
// Checks the rootwallet agent for an existing entry, creates one if not found.
func EnsureVaultEntry(vaultTarget string) error {
	client := newClient()
	ctx := context.Background()

	host, user := parseVaultTarget(vaultTarget)

	// Check if entry already exists
	_, err := client.GetSSHKey(ctx, host, user, "pub")
	if err == nil {
		return nil // entry exists
	}

	// If not found, create it
	if rwagent.IsNotFound(err) {
		_, createErr := client.CreateSSHEntry(ctx, host, user)
		if createErr != nil {
			return wrapAgentError(createErr, fmt.Sprintf("create vault entry %s", vaultTarget))
		}
		return nil
	}

	return wrapAgentError(err, fmt.Sprintf("check vault entry %s", vaultTarget))
}

// ResolveVaultPublicKey returns the OpenSSH public key string for a vault entry.
func ResolveVaultPublicKey(vaultTarget string) (string, error) {
	client := newClient()
	ctx := context.Background()

	host, user := parseVaultTarget(vaultTarget)
	data, err := client.GetSSHKey(ctx, host, user, "pub")
	if err != nil {
		return "", wrapAgentError(err, fmt.Sprintf("get public key for %s", vaultTarget))
	}

	pubKey := strings.TrimSpace(data.PublicKey)
	if !strings.HasPrefix(pubKey, "ssh-") {
		return "", fmt.Errorf("agent returned invalid public key for %s", vaultTarget)
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

// cleanupKeys zero-overwrites and removes all key files, then removes the temp dir.
func cleanupKeys(tmpDir string, keyPaths []string) {
	zeros := make([]byte, 512)
	for _, p := range keyPaths {
		_ = os.WriteFile(p, zeros, 0600) // zero-overwrite
		_ = os.Remove(p)
	}
	_ = os.Remove(tmpDir)
}
