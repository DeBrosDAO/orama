package statuscmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/spf13/cobra"
)

var (
	envFlag  string
	jsonFlag bool
)

// Cmd is the top-level "status" command — health check for operator's nodes.
var Cmd = &cobra.Command{
	Use:   "status",
	Short: "Show health status of your nodes",
	Long: `Check the health of all your nodes in an environment.
SSHes into each node and runs orama node report to collect health data.`,
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
			fmt.Printf("No nodes found for environment %q\n", env)
			return nil
		}

		cleanup, err := remotessh.PrepareNodeKeys(nodes)
		if err != nil {
			return fmt.Errorf("failed to prepare SSH keys: %w", err)
		}
		defer cleanup()

		fmt.Printf("Checking %d node(s) in %s...\n\n", len(nodes), env)

		type nodeResult struct {
			Host   string `json:"host"`
			Role   string `json:"role"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}

		results := make([]nodeResult, len(nodes))
		var wg sync.WaitGroup

		for i, n := range nodes {
			wg.Add(1)
			go func(idx int, node inspector.Node) {
				defer wg.Done()

				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				result := inspector.RunSSH(ctx, node, "sudo orama node report --json")
				nr := nodeResult{Host: node.Host, Role: node.Role}

				if !result.OK() {
					nr.Status = "unreachable"
					nr.Error = fmt.Sprintf("SSH failed (exit %d)", result.ExitCode)
					if result.Stderr != "" {
						nr.Error = result.Stderr
						if len(nr.Error) > 100 {
							nr.Error = nr.Error[:100] + "..."
						}
					}
					results[idx] = nr
					return
				}

				var report struct {
					Gateway struct {
						Responsive bool `json:"responsive"`
					} `json:"gateway"`
					RQLite struct {
						RaftState string `json:"raft_state"`
					} `json:"rqlite"`
				}
				if err := json.Unmarshal([]byte(result.Stdout), &report); err != nil {
					nr.Status = "unknown"
					nr.Error = "failed to parse report"
					results[idx] = nr
					return
				}

				if report.Gateway.Responsive && (report.RQLite.RaftState == "Leader" || report.RQLite.RaftState == "Follower") {
					nr.Status = "healthy"
				} else {
					nr.Status = "degraded"
				}
				results[idx] = nr
			}(i, n)
		}
		wg.Wait()

		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "IP\tROLE\tSTATUS\tDETAILS\n")
		healthy := 0
		for _, r := range results {
			details := r.Error
			if r.Status == "healthy" {
				healthy++
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Host, r.Role, r.Status, details)
		}
		w.Flush()

		fmt.Printf("\n%d/%d nodes healthy\n", healthy, len(results))
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Environment (default: active)")
	Cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
}
