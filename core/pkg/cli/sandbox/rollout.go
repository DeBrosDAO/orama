package sandbox

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RolloutFlags holds optional flags passed through to `orama node upgrade`.
type RolloutFlags struct {
	AnyoneClient bool
}

// Rollout builds, pushes, and performs a rolling upgrade on a sandbox cluster.
func Rollout(name string, flags RolloutFlags) error {
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

	fmt.Printf("Rolling out to sandbox %q (%d nodes)\n\n", state.Name, len(state.Servers))

	// Step 1: Find or require binary archive
	archivePath := build.FindNewestArchive()
	if archivePath == "" {
		return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
	}

	info, _ := os.Stat(archivePath)
	fmt.Printf("Archive: %s (%s)\n\n", filepath.Base(archivePath), printer.FormatBytes(info.Size()))

	// Build extra flags string for upgrade command
	extraFlags := flags.upgradeFlags()

	// Step 2: Push archive to all nodes (upload to first, fan out server-to-server)
	fmt.Println("Pushing archive to all nodes...")
	if err := fanoutArchive(state.Servers, sshKeyPath, archivePath); err != nil {
		return err
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
		if err := upgradeNode(srv, sshKeyPath, i+1, len(state.Servers), extraFlags); err != nil {
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
		if err := upgradeNode(srv, sshKeyPath, len(state.Servers), len(state.Servers), extraFlags); err != nil {
			return err
		}
	}

	fmt.Printf("\nRollout complete for sandbox %q\n", state.Name)
	return nil
}

// upgradeFlags builds the extra CLI flags string for `orama node upgrade`.
func (f RolloutFlags) upgradeFlags() string {
	var parts []string
	if f.AnyoneClient {
		parts = append(parts, "--anyone-client")
	}
	return strings.Join(parts, " ")
}

// findLeaderIndex returns the index of the RQLite leader node, or -1 if unknown.
func findLeaderIndex(state *SandboxState, sshKeyPath string) int {
	for i, srv := range state.Servers {
		node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}
		out, err := runSSHOutput(node, fmt.Sprintf("curl -sf %s/status 2>/dev/null | grep -o '\"state\":\"[^\"]*\"'", constants.LocalRQLiteURL()))
		if err == nil && contains(out, "Leader") {
			return i
		}
	}
	return -1
}

// upgradeNode performs `orama node upgrade --restart` on a single node.
// It pre-replaces the orama CLI binary before running the upgrade command
// to avoid ETXTBSY ("text file busy") errors when the old binary doesn't
// have the os.Remove fix in copyBinary().
func upgradeNode(srv ServerState, sshKeyPath string, current, total int, extraFlags string) error {
	node := inspector.Node{User: "root", Host: srv.IP, SSHKey: sshKeyPath}

	fmt.Printf("  [%d/%d] Upgrading %s (%s)...\n", current, total, srv.Name, srv.IP)

	// Pre-replace the orama CLI so the upgrade runs the NEW binary (with ETXTBSY fix).
	// rm unlinks the old inode (kernel keeps it alive for the running process),
	// cp creates a fresh inode at the same path.
	preReplace := "rm -f /usr/local/bin/orama && cp /opt/orama/bin/orama /usr/local/bin/orama"
	if err := remotessh.RunSSHStreaming(node, preReplace, remotessh.WithNoHostKeyCheck()); err != nil {
		return fmt.Errorf("pre-replace orama binary on %s: %w", srv.Name, err)
	}

	upgradeCmd := "orama node upgrade --restart"
	if extraFlags != "" {
		upgradeCmd += " " + extraFlags
	}
	if err := remotessh.RunSSHStreaming(node, upgradeCmd, remotessh.WithNoHostKeyCheck()); err != nil {
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
