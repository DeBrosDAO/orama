package monitorcmd

import (
	"context"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/monitor"
	"github.com/DeBrosOfficial/network/pkg/cli/monitor/display"
	"github.com/DeBrosOfficial/network/pkg/cli/monitor/tui"
	"github.com/spf13/cobra"
)

// Cmd is the root monitor command.
var Cmd = &cobra.Command{
	Use:   "monitor",
	Short: "Monitor cluster health from your local machine",
	Long: `SSH into cluster nodes and display real-time health data.
Runs 'orama node report --json' on each node and aggregates results.

Without a subcommand, launches the interactive TUI.`,
	RunE: runLive,
}

// Shared persistent flags.
var (
	flagEnv    string
	flagJSON   bool
	flagNode   string
	flagConfig string
)

func init() {
	Cmd.PersistentFlags().StringVar(&flagEnv, "env", "", "Environment: devnet, testnet, mainnet (required)")
	Cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Machine-readable JSON output")
	Cmd.PersistentFlags().StringVar(&flagNode, "node", "", "Filter to specific node host/IP")
	Cmd.PersistentFlags().StringVar(&flagConfig, "config", "scripts/nodes.conf", "Path to nodes.conf")
	Cmd.MarkPersistentFlagRequired("env")

	Cmd.AddCommand(liveCmd)
	Cmd.AddCommand(clusterCmd)
	Cmd.AddCommand(nodeCmd)
	Cmd.AddCommand(serviceCmd)
	Cmd.AddCommand(meshCmd)
	Cmd.AddCommand(dnsCmd)
	Cmd.AddCommand(namespacesCmd)
	Cmd.AddCommand(alertsCmd)
	Cmd.AddCommand(reportCmd)
}

// ---------------------------------------------------------------------------
// Subcommands
// ---------------------------------------------------------------------------

var liveCmd = &cobra.Command{
	Use:   "live",
	Short: "Interactive TUI monitor",
	RunE:  runLive,
}

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster overview (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.ClusterJSON(snap, os.Stdout)
		}
		return display.ClusterTable(snap, os.Stdout)
	},
}

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Per-node health details (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.NodeJSON(snap, os.Stdout)
		}
		return display.NodeTable(snap, os.Stdout)
	},
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Service status across the cluster (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.ServiceJSON(snap, os.Stdout)
		}
		return display.ServiceTable(snap, os.Stdout)
	},
}

var meshCmd = &cobra.Command{
	Use:   "mesh",
	Short: "Mesh connectivity status (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.MeshJSON(snap, os.Stdout)
		}
		return display.MeshTable(snap, os.Stdout)
	},
}

var dnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "DNS health overview (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.DNSJSON(snap, os.Stdout)
		}
		return display.DNSTable(snap, os.Stdout)
	},
}

var namespacesCmd = &cobra.Command{
	Use:   "namespaces",
	Short: "Namespace usage summary (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.NamespacesJSON(snap, os.Stdout)
		}
		return display.NamespacesTable(snap, os.Stdout)
	},
}

var alertsCmd = &cobra.Command{
	Use:   "alerts",
	Short: "Active alerts and warnings (one-shot)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		if flagJSON {
			return display.AlertsJSON(snap, os.Stdout)
		}
		return display.AlertsTable(snap, os.Stdout)
	},
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Full cluster report (JSON)",
	RunE: func(cmd *cobra.Command, args []string) error {
		snap, err := collectSnapshot()
		if err != nil {
			return err
		}
		return display.FullReport(snap, os.Stdout)
	},
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func collectSnapshot() (*monitor.ClusterSnapshot, error) {
	cfg := newConfig()
	return monitor.CollectOnce(context.Background(), cfg)
}

func newConfig() monitor.CollectorConfig {
	return monitor.CollectorConfig{
		ConfigPath: flagConfig,
		Env:        flagEnv,
		NodeFilter: flagNode,
		Timeout:    30 * time.Second,
	}
}

func runLive(cmd *cobra.Command, args []string) error {
	cfg := newConfig()
	return tui.Run(cfg)
}
