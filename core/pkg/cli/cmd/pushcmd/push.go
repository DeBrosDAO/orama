package pushcmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/spf13/cobra"
)

var (
	envFlag    string
	ipFlag     string
	userFlag   string
	fanoutFlag bool
)

// Cmd is the top-level "push" command — upload binary archive to nodes.
var Cmd = &cobra.Command{
	Use:   "push",
	Short: "Push binary archive to your nodes",
	Long: `Upload the pre-built binary archive to nodes and extract it.

By default, uploads from your machine to each node sequentially.
Use --fanout to upload to one node, then fan out server-to-server (faster).

Examples:
  orama push --ip 1.2.3.4                    # Push to one node
  orama push --env devnet                     # Sequential push to all devnet nodes
  orama push --env devnet --fanout            # Fan out server-to-server (faster)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		archivePath := findNewestArchive()
		if archivePath == "" {
			return fmt.Errorf("no binary archive found in /tmp/ (run `orama build` first)")
		}
		info, err := os.Stat(archivePath)
		if err != nil {
			return fmt.Errorf("stat archive: %w", err)
		}
		fmt.Printf("Archive: %s (%s)\n", filepath.Base(archivePath), formatBytes(info.Size()))

		var nodes []inspector.Node

		if ipFlag != "" {
			user := userFlag
			if user == "" {
				user = "root"
			}
			vaultTarget := fmt.Sprintf("%s/%s", ipFlag, user)
			env := envFlag
			if env == "" {
				active, _ := cli.GetActiveEnvironment()
				if active != nil {
					env = active.Name
				}
			}
			if env == "sandbox" {
				vaultTarget = "sandbox/root"
			}
			nodes = []inspector.Node{{
				Host: ipFlag, User: user, VaultTarget: vaultTarget, Environment: env,
			}}
		} else {
			env := envFlag
			if env == "" {
				active, err := cli.GetActiveEnvironment()
				if err != nil {
					return fmt.Errorf("no --ip or --env specified and no active environment")
				}
				env = active.Name
			}
			resolved, err := noderesolver.ResolveNodes(env)
			if err != nil {
				return fmt.Errorf("failed to resolve nodes: %w", err)
			}
			if len(resolved) == 0 {
				return fmt.Errorf("no nodes found for environment %q", env)
			}
			nodes = resolved
		}

		// Prepare SSH keys
		cleanup, err := remotessh.PrepareNodeKeys(nodes)
		if err != nil {
			return fmt.Errorf("failed to prepare SSH keys: %w", err)
		}
		defer cleanup()

		// Single node or default: upload sequentially
		if len(nodes) == 1 || !fanoutFlag {
			return pushDirect(nodes, archivePath)
		}

		// Multi-node with --fanout: use agent forwarding
		return pushFanout(nodes, archivePath)
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Target environment (default: active)")
	Cmd.Flags().StringVar(&ipFlag, "ip", "", "Push to a single node by IP")
	Cmd.Flags().StringVar(&userFlag, "user", "", "SSH user (default: root)")
	Cmd.Flags().BoolVar(&fanoutFlag, "fanout", false, "Upload to first node, then fan out server-to-server (faster)")
}

// pushDirect uploads the archive from local machine to each node sequentially.
func pushDirect(nodes []inspector.Node, archivePath string) error {
	fmt.Printf("Pushing to %d node(s) (direct)...\n\n", len(nodes))

	remotePath := "/tmp/" + filepath.Base(archivePath)
	extractCmd := fmt.Sprintf("sudo bash -c 'mkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s && /opt/orama/bin/orama version'", remotePath, remotePath)

	for _, n := range nodes {
		fmt.Printf("  %s: uploading...", n.Host)
		if err := remotessh.UploadFile(n, archivePath, remotePath); err != nil {
			fmt.Printf(" FAILED (%v)\n", err)
			continue
		}
		fmt.Printf(" extracting...")
		if err := remotessh.RunSSHStreaming(n, extractCmd); err != nil {
			fmt.Printf(" FAILED (%v)\n", err)
			continue
		}
		fmt.Println(" OK")
	}

	fmt.Println("\nPush complete")
	return nil
}

// pushFanout uploads the archive to the first node, then fans out server-to-server
// using SSH agent forwarding.
func pushFanout(nodes []inspector.Node, archivePath string) error {
	fmt.Printf("Pushing to %d node(s) (fanout)...\n\n", len(nodes))

	hub := nodes[0]
	targets := nodes[1:]
	remotePath := "/tmp/" + filepath.Base(archivePath)
	// Hub extraction keeps the archive so it can be fanned out to targets.
	// The cleanup at the end removes it.
	hubExtractCmd := fmt.Sprintf("mkdir -p /opt/orama && tar xzf %s -C /opt/orama", remotePath)
	// Target extraction deletes the archive after extracting.
	targetExtractCmd := fmt.Sprintf("mkdir -p /opt/orama && tar xzf %s -C /opt/orama && rm -f %s", remotePath, remotePath)

	// Load SSH keys into the system ssh-agent for agent forwarding
	fmt.Println("  Loading SSH keys into agent...")
	if err := remotessh.LoadAgentKeys(nodes); err != nil {
		fmt.Printf("  Warning: failed to load agent keys: %v\n", err)
		fmt.Println("  Falling back to direct push...")
		return pushDirect(nodes, archivePath)
	}

	// Upload archive to hub
	fmt.Printf("  %s (hub): uploading...", hub.Host)
	if err := remotessh.UploadFile(hub, archivePath, remotePath); err != nil {
		return fmt.Errorf("failed to upload to hub %s: %w", hub.Host, err)
	}
	fmt.Printf(" extracting...")
	if err := remotessh.RunSSHStreaming(hub, "sudo bash -c '"+hubExtractCmd+"'"); err != nil {
		return fmt.Errorf("failed to extract on hub %s: %w", hub.Host, err)
	}
	fmt.Println(" OK")

	// Build the fanout command — hub SCPs to all targets in parallel
	var fanoutParts []string
	for _, t := range targets {
		scpCmd := fmt.Sprintf(
			"scp -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=no %s %s@%s:%s && ssh -o StrictHostKeyChecking=accept-new %s@%s 'sudo bash -c \"%s\"' && echo '%s: done'",
			remotePath, t.User, t.Host, remotePath,
			t.User, t.Host, targetExtractCmd,
			t.Host,
		)
		fanoutParts = append(fanoutParts, "("+scpCmd+") &")
	}
	fanoutParts = append(fanoutParts, "wait", "echo 'Fanout complete'")
	fanoutScript := strings.Join(fanoutParts, "\n")

	// Base64-encode the script to avoid shell quoting conflicts — the script
	// contains single quotes (ssh '...') that would break a bash -c '...' wrapper.
	encoded := base64.StdEncoding.EncodeToString([]byte(fanoutScript))
	runCmd := fmt.Sprintf("echo %s | base64 -d | bash", encoded)

	fmt.Printf("  Fanning out to %d nodes from %s...\n", len(targets), hub.Host)
	if err := remotessh.RunSSHStreaming(hub, runCmd, remotessh.WithAgentForward()); err != nil {
		fmt.Printf("  Fanout failed: %v\n", err)
		fmt.Println("  Some nodes may not have been updated")
	}

	// Clean up archive on hub
	remotessh.RunSSHStreaming(hub, "rm -f "+remotePath)

	fmt.Println("\nPush complete")
	return nil
}

func findNewestArchive() string {
	matches, _ := filepath.Glob("/tmp/orama-*-linux-*.tar.gz")
	if len(matches) == 0 {
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
