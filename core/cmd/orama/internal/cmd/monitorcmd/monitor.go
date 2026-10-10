// Package monitorcmd provides `orama monitor`: the cluster's health from
// the operator's machine, live or one view at a time.
package monitorcmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/tui"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Cmd is the root monitor command.
var Cmd = &cobra.Command{
	Use:   "monitor",
	Short: "Monitor cluster health from your local machine",
	Long: `Show the cluster's health: a live view, or one aspect at a time.

The data comes from the gateway's operator telemetry API
(GET /v1/operator/telemetry, and its server-sent event stream for the live
view), authenticated with the credentials 'orama auth login' stored for the
environment's gateway. Only the cluster's operators may read it.

--ssh is the break-glass path for when no gateway answers: it SSHes into every
node and runs 'sudo orama node report' there instead. It is never chosen
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
	Cmd.PersistentFlags().StringVar(&flagEnv, "env", "", "Environment (default: active)")
	Cmd.PersistentFlags().StringVar(&flagNode, "node", "", "Show only this node (public IP or WireGuard IP)")
	Cmd.PersistentFlags().BoolVar(&flagSSH, "ssh", false, "Collect over SSH from every node instead of the gateway API (break-glass)")
	Cmd.PersistentFlags().StringVar(&flagConfig, "config", "", "With --ssh: read nodes from this file instead of resolving them")
	Cmd.Flags().DurationVar(&flagInterval, "interval", monitor.DefaultInterval, intervalUsage)
	liveCmd.Flags().DurationVar(&flagInterval, "interval", monitor.DefaultInterval, intervalUsage)

	Cmd.AddCommand(liveCmd)
	for _, v := range oneShots {
		Cmd.AddCommand(newOneShotCmd(v))
	}
}

var liveCmd = &cobra.Command{
	Use:   "live",
	Short: "Interactive live view (the default)",
	RunE:  runLive,
}

// oneShot is a subcommand that reads one snapshot and prints one view of it.
type oneShot struct {
	use, short string
	table      func(*cluster.ClusterSnapshot, io.Writer) error
	json       func(*cluster.ClusterSnapshot, io.Writer) error
}

var oneShots = []oneShot{
	{"cluster", "Verdict, components and a row per node (one-shot)", display.ClusterTable, display.ClusterJSON},
	{"node", "Per-node health details (one-shot)", display.NodeTable, display.NodeJSON},
	{"service", "Service status across the cluster (one-shot)", display.ServiceTable, display.ServiceJSON},
	{"mesh", "WireGuard mesh connectivity (one-shot)", display.MeshTable, display.MeshJSON},
	{"dns", "DNS and TLS health of the nameservers (one-shot)", display.DNSTable, display.DNSJSON},
	{"namespaces", "Namespace health across nodes (one-shot)", display.NamespacesTable, display.NamespacesJSON},
	{"alerts", "Alerts, most severe first, with what to do (one-shot)", display.AlertsTable, display.AlertsJSON},
	{"traffic", "Gateway requests, errors and latency (one-shot)", display.TrafficTable, display.TrafficJSON},
	{"chain", "Orama L1 height, sync and validators (one-shot)", display.ChainTable, display.ChainJSON},
	{"report", "Full cluster report as JSON (one-shot)", display.FullReport, display.FullReport},
}

func newOneShotCmd(v oneShot) *cobra.Command {
	return &cobra.Command{
		Use:   v.use,
		Short: v.short,
		RunE: func(cmd *cobra.Command, args []string) error {
			src, _, err := newSource()
			if err != nil {
				return err
			}
			snap, err := src.Snapshot(cmd.Context())
			if err != nil {
				return err
			}
			if printer.For(cmd).JSONMode() {
				return v.json(snap, os.Stdout)
			}
			return v.table(snap, os.Stdout)
		},
	}
}

// resolveEnv is the environment --env names, or the active one when it is left
// out, as `orama status` does.
func resolveEnv() (string, error) {
	if flagEnv != "" {
		return flagEnv, nil
	}
	active, err := cli.GetActiveEnvironment()
	if err != nil {
		return "", fmt.Errorf("no --env given and no active environment: %w", err)
	}
	return active.Name, nil
}

// newSource returns the snapshot source and the environment it reads.
func newSource() (monitor.Source, string, error) {
	env, err := resolveEnv()
	if err != nil {
		return nil, "", err
	}
	src, err := monitor.NewSource(monitor.Options{
		Env:        env,
		Node:       flagNode,
		ConfigPath: flagConfig,
		SSH:        flagSSH,
		SSHTimeout: monitor.DefaultSSHTimeout,
	})
	return src, env, err
}

func runLive(cmd *cobra.Command, args []string) error {
	interval, err := monitor.ResolveInterval(flagInterval, cmd.Flags().Changed("interval"), flagSSH)
	if err != nil {
		return err
	}
	src, env, err := newSource()
	if err != nil {
		return err
	}
	return tui.Run(tui.Config{Source: src, Env: env, Interval: interval})
}
