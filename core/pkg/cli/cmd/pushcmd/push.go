package pushcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/spf13/cobra"
)

var (
	envFlag  string
	ipFlag   string
	userFlag string
)

// Cmd is the top-level "push" command — upload binary archive to nodes.
var Cmd = &cobra.Command{
	Use:   "push",
	Short: "Push binary archive to your nodes",
	Long: `Upload the pre-built binary archive to nodes and extract it.

Use --ip to push to a single node, or omit it to push to all nodes
in the active environment.

Examples:
  orama push --ip 1.2.3.4                    # Push to one node
  orama push --ip 1.2.3.4 --user ubuntu      # Push with specific SSH user
  orama push --env devnet                     # Push to all devnet nodes
  orama push --env devnet --ip 1.2.3.4       # Push to one devnet node`,
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
			// Single node push
			user := userFlag
			if user == "" {
				user = "root"
			}
			vaultTarget := fmt.Sprintf("%s/%s", ipFlag, user)
			env := envFlag
			if env == "" {
				active, err := cli.GetActiveEnvironment()
				if err == nil {
					env = active.Name
				}
			}
			if env == "sandbox" {
				vaultTarget = "sandbox/root"
			}
			nodes = []inspector.Node{{
				Host:        ipFlag,
				User:        user,
				VaultTarget: vaultTarget,
				Environment: env,
			}}
		} else {
			// All nodes in environment
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

		fmt.Printf("Pushing to %d node(s)...\n\n", len(nodes))

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
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Target environment (default: active)")
	Cmd.Flags().StringVar(&ipFlag, "ip", "", "Push to a single node by IP")
	Cmd.Flags().StringVar(&userFlag, "user", "", "SSH user (default: root)")
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
