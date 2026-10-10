// Package monitorcmd provides `orama monitor`: the cluster's health from
// the operator's machine, live or one view at a time.
package monitorcmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/tui"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Cmd is the root monitor command. It is hidden: `orama status` is the one view an operator opens,
// and its subcommands are these same views. monitor stays for the scripts that still call it.
var Cmd = &cobra.Command{
	Use:    "monitor",
	Hidden: true,
	Short:  "Monitor cluster health from your local machine (use orama status)",
	Long: `Show the cluster's health: a live view, or one aspect at a time.

The data comes from the gateway's operator telemetry API
(GET /v1/operator/telemetry, and its server-sent event stream for the live
view), authenticated with the credentials 'orama auth login' stored for the
environment's gateway. Only the cluster's operators may read it.

--ssh is the break-glass path for when no gateway answers: it SSHes into every
node and runs 'sudo orama node report --json' there instead. It is never chosen
automatically; when the API fails the error says so and suggests it. Traffic is
counted by the gateways, so it is empty over --ssh.

Without a subcommand, opens the live view. Every view starts with the verdict:
"✓ All systems operational" or what is degraded, with the alert counts and the
age of the data. Live view keys: tab/shift+tab or 1-9 switch tabs, ↑/↓ (j/k)
select or scroll, enter opens a node's full report on the Nodes tab, esc goes
back, c/w/i/a filter the Alerts tab by severity, r refreshes, ? shows help,
q quits.`,
	// A positional argument can only be a view name that does not exist; it
	// was ignored and the live view opened anyway, so a typo looked like
	// success (and without a terminal, failed on the TTY).
	Args: cobra.NoArgs,
	RunE: runLive,
}

// Flags. --json is a persistent flag on the root, so it is not defined here.
var (
	flagEnv      string
	flagNode     string
	flagConfig   string
	flagSSH      bool
	flagInterval = monitor.DefaultInterval
)

var intervalUsage = fmt.Sprintf("How often the live view refreshes, %.0fs to %.0fs (with --ssh: at least %.0fs, which is also its default)",
	monitor.MinAPIInterval.Seconds(), monitor.MaxAPIInterval.Seconds(), monitor.MinSSHInterval.Seconds())

func init() {
	Cmd.PersistentFlags().StringVar(&flagEnv, "env", "", "Environment: devnet, testnet, mainnet (required)")
	Cmd.PersistentFlags().StringVar(&flagNode, "node", "", "Show only this node (public IP or WireGuard IP)")
	Cmd.PersistentFlags().BoolVar(&flagSSH, "ssh", false, "Collect over SSH from every node instead of the gateway API (break-glass)")
	Cmd.PersistentFlags().StringVar(&flagConfig, "config", "", "With --ssh: read nodes from this file instead of resolving them")
	Cmd.MarkPersistentFlagRequired("env")
	Cmd.Flags().DurationVar(&flagInterval, "interval", monitor.DefaultInterval, intervalUsage)
	liveCmd.Flags().DurationVar(&flagInterval, "interval", monitor.DefaultInterval, intervalUsage)

	Cmd.AddCommand(liveCmd)
	for _, v := range Views {
		Cmd.AddCommand(ViewCommand(v, newSource))
	}
}

var liveCmd = &cobra.Command{
	Use:   "live",
	Short: "Interactive live view (the default)",
	RunE:  runLive,
}

// View is a subcommand that reads one snapshot and prints one view of it. `orama status` mounts
// the same list.
type View struct {
	Use, Short string
	Table      func(*cluster.ClusterSnapshot, io.Writer) error
	JSON       func(*cluster.ClusterSnapshot, io.Writer) error
}

// Views are the one-shot views of a snapshot.
var Views = []View{
	{Use: "cluster", Short: "Verdict, components and a row per node (one-shot)", Table: display.ClusterTable, JSON: display.ClusterJSON},
	{Use: "node", Short: "Per-node health details (one-shot)", Table: display.NodeTable, JSON: display.NodeJSON},
	{Use: "service", Short: "Service status across the cluster (one-shot)", Table: display.ServiceTable, JSON: display.ServiceJSON},
	{Use: "mesh", Short: "WireGuard mesh connectivity (one-shot)", Table: display.MeshTable, JSON: display.MeshJSON},
	{Use: "dns", Short: "DNS and TLS health of the nameservers (one-shot)", Table: display.DNSTable, JSON: display.DNSJSON},
	{Use: "namespaces", Short: "Namespace health across nodes (one-shot)", Table: display.NamespacesTable, JSON: display.NamespacesJSON},
	{Use: "alerts", Short: "Alerts, most severe first, with what to do (one-shot)", Table: display.AlertsTable, JSON: display.AlertsJSON},
	{Use: "traffic", Short: "Gateway requests, errors and latency (one-shot)", Table: display.TrafficTable, JSON: display.TrafficJSON},
	{Use: "chain", Short: "Orama L1 height, sync and validators (one-shot)", Table: display.ChainTable, JSON: display.ChainJSON},
	{Use: "report", Short: "Full cluster report as JSON (one-shot)", Table: display.FullReport, JSON: display.FullReport},
}

// ViewCommand is the subcommand that prints v of the snapshot source makes.
func ViewCommand(v View, source func() (monitor.Source, error)) *cobra.Command {
	return &cobra.Command{
		Use:   v.Use,
		Short: v.Short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source()
			if err != nil {
				return err
			}
			snap, err := src.Snapshot(cmd.Context())
			if err != nil {
				return err
			}
			if printer.For(cmd).JSONMode() {
				return v.JSON(snap, os.Stdout)
			}
			return v.Table(snap, os.Stdout)
		},
	}
}

func newSource() (monitor.Source, error) {
	return monitor.NewSource(monitor.Options{
		Env:        flagEnv,
		Node:       flagNode,
		ConfigPath: flagConfig,
		SSH:        flagSSH,
		SSHTimeout: monitor.DefaultSSHTimeout,
	})
}

func runLive(cmd *cobra.Command, args []string) error {
	interval, err := monitor.ResolveInterval(flagInterval, cmd.Flags().Changed("interval"), flagSSH)
	if err != nil {
		return err
	}
	src, err := newSource()
	if err != nil {
		return err
	}
	return tui.Run(tui.Config{Source: src, Env: flagEnv, Interval: interval})
}
