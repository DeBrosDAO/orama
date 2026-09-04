package inspectcmd

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the inspect command for SSH-based cluster inspection.
var inspectOpts cli.InspectOptions

var Cmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect cluster health via SSH",
	Long: `SSH into cluster nodes and run health checks.
Supports AI-powered failure analysis and result export.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.RunInspect(inspectOpts)
	},
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&inspectOpts.ConfigPath, "config", "scripts/nodes.conf", "Path to nodes.conf")
	f.StringVar(&inspectOpts.Env, "env", "", "Environment to inspect (devnet, testnet)")
	f.StringVar(&inspectOpts.Subsystem, "subsystem", "all", "Subsystem to inspect (rqlite,olric,ipfs,dns,wg,system,network,anyone,all)")
	f.StringVar(&inspectOpts.Format, "format", "table", "Output format (table, json)")
	f.DurationVar(&inspectOpts.Timeout, "timeout", 30*time.Second, "SSH command timeout")
	f.BoolVar(&inspectOpts.Verbose, "verbose", false, "Verbose output")
	f.StringVar(&inspectOpts.OutputDir, "output", "", "Save results to directory as markdown (e.g., ./results)")
	f.BoolVar(&inspectOpts.AIEnabled, "ai", false, "Enable AI analysis of failures")
	f.StringVar(&inspectOpts.AIModel, "model", "moonshotai/kimi-k2.5", "OpenRouter model for AI analysis")
	f.StringVar(&inspectOpts.AIAPIKey, "api-key", "", "OpenRouter API key (or OPENROUTER_API_KEY env)")
}
