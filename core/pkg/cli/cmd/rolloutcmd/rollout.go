package rolloutcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/spf13/cobra"
)

var (
	envFlag  string
	delaySec int
)

// Cmd is the top-level "rollout" command — build + push + rolling upgrade.
var Cmd = &cobra.Command{
	Use:   "rollout",
	Short: "Rolling upgrade of your nodes",
	Long: `Build, push, and perform a rolling upgrade on all your nodes in an environment.
Upgrades followers first, leader last, with health checks between each node.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env := envFlag
		if env == "" {
			active, err := cli.GetActiveEnvironment()
			if err != nil {
				return fmt.Errorf("failed to get active environment: %w", err)
			}
			env = active.Name
		}

		nodes, err := noderesolver.ResolveNodes(env)
		if err != nil {
			return fmt.Errorf("failed to resolve nodes: %w", err)
		}
		if len(nodes) == 0 {
			return fmt.Errorf("no nodes found for environment %q", env)
		}

		cleanup, err := remotessh.PrepareNodeKeys(nodes)
		if err != nil {
			return fmt.Errorf("failed to prepare SSH keys: %w", err)
		}
		defer cleanup()

		fmt.Printf("Rolling out to %d node(s) in %s\n\n", len(nodes), env)

		// Step 1: Find archive
		archivePath := findNewestArchive()
		if archivePath == "" {
			return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
		}
		info, err := os.Stat(archivePath)
		if err != nil {
			return fmt.Errorf("stat archive %s: %w", archivePath, err)
		}
		fmt.Printf("Archive: %s (%s)\n\n", filepath.Base(archivePath), formatBytes(info.Size()))

		// Step 2: Push archive to all nodes
		fmt.Println("Pushing archive to all nodes...")
		if err := pushArchive(nodes, archivePath); err != nil {
			return err
		}

		// Step 3: Rolling upgrade — followers first, leader last
		fmt.Println("\nRolling upgrade (followers first, leader last)...")

		leaderIdx := findLeaderIndex(nodes)
		if leaderIdx < 0 {
			fmt.Fprintf(os.Stderr, "  Warning: could not detect RQLite leader, upgrading in order\n")
		}

		// Determine SSH options based on environment
		var sshOpts []remotessh.SSHOption
		if env == "sandbox" {
			sshOpts = append(sshOpts, remotessh.WithNoHostKeyCheck())
		}

		delay := time.Duration(delaySec) * time.Second

		// Upgrade non-leaders first
		count := 0
		for i := range nodes {
			if i == leaderIdx {
				continue
			}
			count++
			if err := upgradeNode(nodes[i], count, len(nodes), sshOpts); err != nil {
				return err
			}
			if count < len(nodes) {
				fmt.Printf("  Waiting %s before next node...\n", delay)
				time.Sleep(delay)
			}
		}

		// Upgrade leader last
		if leaderIdx >= 0 {
			count++
			if err := upgradeNode(nodes[leaderIdx], count, len(nodes), sshOpts); err != nil {
				return err
			}
		}

		fmt.Printf("\nRollout complete for %s (%d nodes)\n", env, len(nodes))
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment (default: active)")
	Cmd.Flags().IntVar(&delaySec, "delay", 30, "Seconds to wait between node upgrades")
}

// findLeaderIndex returns the index of the RQLite leader, or -1 if unknown.
func findLeaderIndex(nodes []inspector.Node) int {
	for i, n := range nodes {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result := inspector.RunSSH(ctx, n, fmt.Sprintf("curl -sf %s/status 2>/dev/null | grep -o '\"state\":\"[^\"]*\"'", constants.LocalRQLiteURL()))
		cancel()
		if result.OK() && strings.Contains(result.Stdout, "Leader") {
			return i
		}
	}
	return -1
}

// upgradeNode performs orama node upgrade --restart on a single node.
func upgradeNode(node inspector.Node, current, total int, sshOpts []remotessh.SSHOption) error {
	fmt.Printf("  [%d/%d] Upgrading %s...\n", current, total, node.Host)

	// /usr/local/bin/orama is owned by the service account and `orama node upgrade`
	// requires root, so a non-root SSH user (e.g. `ubuntu`) must sudo. The
	// production/upgrade/remote.go path already does this; rolloutcmd previously did
	// not, so the rolling upgrade failed on every non-root node.
	sudo := remotessh.SudoPrefix(node)

	// Pre-replace orama CLI binary to avoid ETXTBSY
	preReplace := fmt.Sprintf("%srm -f /usr/local/bin/orama && %scp /opt/orama/bin/orama /usr/local/bin/orama", sudo, sudo)
	if err := remotessh.RunSSHStreaming(node, preReplace, sshOpts...); err != nil {
		return fmt.Errorf("pre-replace orama binary on %s: %w", node.Host, err)
	}

	if err := remotessh.RunSSHStreaming(node, sudo+"orama node upgrade --restart", sshOpts...); err != nil {
		return fmt.Errorf("upgrade %s: %w", node.Host, err)
	}

	// Wait for health
	fmt.Printf("  Checking health...")
	if err := waitForHealth(node, 2*time.Minute); err != nil {
		fmt.Printf(" WARN: %v\n", err)
	} else {
		fmt.Println(" OK")
	}

	return nil
}

// pushArchive uploads the archive to the first node, then fans out server-to-server.
func pushArchive(nodes []inspector.Node, archivePath string) error {
	if len(nodes) == 0 {
		return nil
	}

	remotePath := "/tmp/" + filepath.Base(archivePath)

	// Upload to first node
	hub := nodes[0]
	fmt.Printf("  Uploading to %s...\n", hub.Host)
	if err := remotessh.UploadFile(hub, archivePath, remotePath); err != nil {
		return fmt.Errorf("upload to %s: %w", hub.Host, err)
	}

	// Build the extract command for a node, applying sudo for non-root users.
	// /opt/orama is owned by the `orama` service account, so a non-root SSH user
	// (e.g. `ubuntu`) must sudo to overwrite it. Previously this path had no sudo
	// (unlike production/push's extractOnNode), so the rollout failed on every
	// non-root node with "tar: Cannot open: File exists / Operation not permitted".
	extractFor := func(node inspector.Node) string {
		s := remotessh.SudoPrefix(node)
		return fmt.Sprintf("%smkdir -p /opt/orama && %star xzf %s -C /opt/orama && %srm -f %s",
			s, s, remotePath, s, remotePath)
	}

	// Extract on hub
	if err := remotessh.RunSSHStreaming(hub, extractFor(hub)); err != nil {
		return fmt.Errorf("extract on %s: %w", hub.Host, err)
	}

	// For remaining nodes, upload directly and extract
	for _, n := range nodes[1:] {
		fmt.Printf("  Uploading to %s...\n", n.Host)
		if err := remotessh.UploadFile(n, archivePath, remotePath); err != nil {
			return fmt.Errorf("upload to %s: %w", n.Host, err)
		}
		if err := remotessh.RunSSHStreaming(n, extractFor(n)); err != nil {
			return fmt.Errorf("extract on %s: %w", n.Host, err)
		}
	}

	return nil
}

// waitForHealth polls RQLite health on a node until it reaches Leader or Follower state.
func waitForHealth(node inspector.Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result := inspector.RunSSH(ctx, node, fmt.Sprintf("curl -sf %s/status 2>/dev/null | grep -o '\"state\":\"[^\"]*\"'", constants.LocalRQLiteURL()))
		cancel()
		if result.OK() && (strings.Contains(result.Stdout, "Leader") || strings.Contains(result.Stdout, "Follower")) {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("timed out waiting for healthy state on %s", node.Host)
}

// findNewestArchive finds the newest orama binary archive in /tmp/.
func findNewestArchive() string {
	matches, err := filepath.Glob("/tmp/orama-*-linux-*.tar.gz")
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0]
}

func formatBytes(b int64) string {
	const mb = 1024 * 1024
	if b >= mb {
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	}
	return fmt.Sprintf("%d KB", b/1024)
}
