package nodescmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/noderesolver"
	"github.com/spf13/cobra"
)

var envFlag string

// Cmd is the top-level "nodes" command — lists operator's nodes.
var Cmd = &cobra.Command{
	Use:   "nodes",
	Short: "List your nodes across environments",
	Long: `List all nodes owned by your wallet. Queries the network API
with your stored credentials, falling back to nodes.conf.

Requires: orama auth login (for API-based resolution)`,
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
			fmt.Printf("Register nodes with: orama node setup --ip <ip> --env %s\n", env)
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "IP\tROLE\tUSER\tENVIRONMENT\n")
		for _, n := range nodes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n.Host, n.Role, n.User, n.Environment)
		}
		w.Flush()

		fmt.Printf("\n%d node(s) in %s\n", len(nodes), env)
		return nil
	},
}

func init() {
	Cmd.Flags().StringVar(&envFlag, "env", "", "Filter by environment (default: active environment)")
}
