package inspectcmd

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/noderesolver"
	"github.com/spf13/cobra"
)

// Cmd is the inspect command for SSH-based cluster inspection.
var inspectOpts cli.InspectOptions

var Cmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect cluster health via SSH",
	Long: `SSH into cluster nodes and run health checks.
Supports AI-powered failure analysis and result export.

The report is written to stdout and progress to stderr, so --format json is one
JSON document. A bad flag value (an unknown --subsystem or --format, a timeout
that is not positive) is refused as usage before any node is contacted.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Before resolving nodes, which asks the gateway: a mistake on the
		// command line is refused without a round trip.
		if err := inspectOpts.Validate(); err != nil {
			return err
		}
		if inspectOpts.ConfigPath == "" {
			nodes, err := noderesolver.ResolveNodes(inspectOpts.Env)
			if err != nil {
				return fmt.Errorf("resolve nodes for %q: %w", inspectOpts.Env, err)
			}
			inspectOpts.Nodes = nodes
		}
		return cli.RunInspect(inspectOpts)
	},
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&inspectOpts.ConfigPath, "config", "", "Read nodes from this file instead of resolving them")
	f.StringVar(&inspectOpts.Env, "env", "", "Environment to inspect (devnet, testnet)")
	f.StringVar(&inspectOpts.Subsystem, "subsystem", "all", "Subsystem to inspect (rqlite,olric,ipfs,dns,wg,system,network,tor,global,all)")
	f.StringVar(&inspectOpts.Format, "format", "table", "Output format (table, json)")
	f.DurationVar(&inspectOpts.Timeout, "timeout", 30*time.Second, "SSH command timeout")
	f.BoolVar(&inspectOpts.Verbose, "verbose", false, "Verbose output")
	f.StringVar(&inspectOpts.OutputDir, "output", "", "Save results to directory as markdown (e.g., ./results)")
	f.BoolVar(&inspectOpts.AIEnabled, "ai", false, "Enable AI analysis of failures")
	f.StringVar(&inspectOpts.AIModel, "model", "moonshotai/kimi-k2.5", "OpenRouter model for AI analysis")
	f.StringVar(&inspectOpts.AIAPIKey, "api-key", "", "OpenRouter API key (or OPENROUTER_API_KEY env)")
}
