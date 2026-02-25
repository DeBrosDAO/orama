package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Rollout builds, pushes, and performs a rolling upgrade on a sandbox cluster.
func Rollout(name string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	state, err := resolveSandbox(name)
	if err != nil {
		return err
	}

	sshKeyPath := cfg.ExpandedPrivateKeyPath()
	fmt.Printf("Rolling out to sandbox %q (%d nodes)\n\n", state.Name, len(state.Servers))

	// Step 1: Find or require binary archive
	archivePath := findNewestArchive()
	if archivePath == "" {
		return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
	}

	info, _ := os.Stat(archivePath)
	fmt.Printf("Archive: %s (%s)\n\n", filepath.Base(archivePath), formatBytes(info.Size()))

	// Step 2: Push archive to all nodes
	fmt.Println("Pushing archive to all nodes...")
	remotePath := "/tmp/" + filepath.Base(archivePath)

	for i, srv := range state.Servers {
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

		fmt.Printf("  [%d/%d] Uploading to %s...\n", i+1, len(state.Servers), srv.Name)
		if err := remotessh.UploadFile(node, archivePath, remotePath); err != nil {
			return fmt.Errorf("upload to %s: %w", srv.Name, err)
		}

		// Extract archive
		extractCmd := fmt.Sprintf("mkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s",
			remotePath, remotePath)
		if err := remotessh.RunSSHStreaming(node, extractCmd); err != nil {
			return fmt.Errorf("extract on %s: %w", srv.Name, err)
		}
	}

	// Step 3: Rolling upgrade — followers first, leader last
	fmt.Println("\nRolling upgrade (followers first, leader last)...")

	// Find the leader
	leaderIdx := findLeaderIndex(state, sshKeyPath)
	if leaderIdx < 0 {
		fmt.Fprintf(os.Stderr, "  Warning: could not detect RQLite leader, upgrading in order\n")
	}

	// Upgrade non-leaders first
	for i, srv := range state.Servers {
		if i == leaderIdx {
			continue // skip leader, do it last
		}
		if err := upgradeNode(srv, sshKeyPath, i+1, len(state.Servers)); err != nil {
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
		if err := upgradeNode(srv, sshKeyPath, len(state.Servers), len(state.Servers)); err != nil {
			return err
		}
	}

	fmt.Printf("\nRollout complete for sandbox %q\n", state.Name)
	return nil
}

// findLeaderIndex returns the index of the RQLite leader node, or -1 if unknown.
func findLeaderIndex(state *SandboxState, sshKeyPath string) int {
	for i, srv := range state.Servers {
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}
		out, err := runSSHOutput(node, "curl -sf http://localhost:5001/status 2>/dev/null | grep -o '\"state\":\"[^\"]*\"'")
		if err == nil && contains(out, "Leader") {
			return i
		}
	}
	return -1
}

// upgradeNode performs `orama node upgrade --restart` on a single node.
func upgradeNode(srv ServerState, sshKeyPath string, current, total int) error {
	node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

	fmt.Printf("  [%d/%d] Upgrading %s (%s)...\n", current, total, srv.Name, srv.IP)
	if err := remotessh.RunSSHStreaming(node, "orama node upgrade --restart"); err != nil {
		return fmt.Errorf("upgrade %s: %w", srv.Name, err)
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
