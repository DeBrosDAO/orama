package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Rollout builds, pushes, and performs a rolling upgrade on a sandbox cluster.
func Rollout(name, archive string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	state, err := resolveSandbox(name)
	if err != nil {
		return err
	}

	sshKeyPath, cleanup, err := resolveVaultKeyOnce(cfg.SSHKey.VaultTarget)
	if err != nil {
		return fmt.Errorf("prepare SSH key: %w", err)
	}
	defer cleanup()

	// Every connection below checks the host keys pinned at create; a sandbox
	// without them is refused before anything is built.
	nodes, err := pinnedNodes(state, sshKeyPath)
	if err != nil {
		return err
	}

	fmt.Printf("Rolling out to sandbox %q (%d nodes)\n\n", state.Name, len(state.Servers))

	// Step 1: The build to roll out: the one named, or one built now.
	archivePath, err := resolveArchive(archive)
	if err != nil {
		return err
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("archive %s: %w", archivePath, err)
	}
	fmt.Printf("Archive: %s (%s)\n\n", filepath.Base(archivePath), printer.FormatBytes(info.Size()))

	// Step 2: Push the archive the way `orama push` does: to a hub that fans
	// it out, and staged on each node by its installed orama, which verifies
	// it against the node's trust anchor before anything changes.
	fmt.Println("Pushing archive to all nodes...")
	if err := pushToSandbox(nodes, archivePath); err != nil {
		return err
	}

	// Step 3: Rolling upgrade — followers first, leader last
	fmt.Println("\nRolling upgrade (followers first, leader last)...")

	// Find the leader
	leaderIdx := findLeaderIndex(nodes)
	if leaderIdx < 0 {
		fmt.Fprintf(os.Stderr, "  Warning: could not detect RQLite leader, upgrading in order\n")
	}

	// Upgrade non-leaders first
	for i, srv := range state.Servers {
		if i == leaderIdx {
			continue // skip leader, do it last
		}
		if err := upgradeNode(nodes[i], srv.Name, i+1, len(state.Servers)); err != nil {
			return err
		}
		// Wait between nodes
		if i < len(state.Servers)-1 {
			fmt.Printf("  Waiting 15s before next node...\n")
			time.Sleep(15 * time.Second)
		}
	}

	// Upgrade leader last
	if leaderIdx >= 0 {
		srv := state.Servers[leaderIdx]
		if err := upgradeNode(nodes[leaderIdx], srv.Name, len(state.Servers), len(state.Servers)); err != nil {
			return err
		}
	}

	fmt.Printf("\nRollout complete for sandbox %q\n", state.Name)
	return nil
}

// findLeaderIndex returns the index of the RQLite leader node, or -1 if unknown.
func findLeaderIndex(nodes []inspector.Node) int {
	for i, node := range nodes {
		out, err := remotessh.RunSSHOutput(node, rqlite.NodeShellCurl(remotessh.SudoPrefix(node), "-sf", "/status")+` 2>/dev/null | grep -o '"state":"[^"]*"'`)
		if err == nil && contains(out, "Leader") {
			return i
		}
	}
	return -1
}

// upgradeNode performs `orama node upgrade --restart` on a single node, as a
// production rolling upgrade does. The upgrade verifies the staged archive
// against the node's trust anchor and installs the CLI on the PATH from it;
// nothing else copies binaries out of /opt/orama.
func upgradeNode(node inspector.Node, name string, current, total int) error {
	fmt.Printf("  [%d/%d] Upgrading %s (%s)...\n", current, total, name, node.Host)

	const upgradeCmd = "orama node upgrade --restart"
	if err := remotessh.RunSSHStreaming(node, upgradeCmd); err != nil {
		return fmt.Errorf("upgrade %s: %w", name, err)
	}

	// Wait for health
	fmt.Printf("  Checking health...")
	if err := waitForRQLiteHealth(node, 2*time.Minute); err != nil {
		fmt.Printf(" WARN: %v\n", err)
	} else {
		fmt.Println(" OK")
	}

	return nil
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
