// Package statuscmd provides `orama status`, the one view an operator opens: every node's cluster
// health and chain, the verdict with what to do, and the operator's own account on the chain.
//
// In a terminal it is the live view; piped, or with --once, it prints one table; with --json it
// prints the document scripts and the newcomer acceptance test check ("healthy"). Its subcommands
// are one aspect at a time (cluster, chain, alerts, ...). The cluster half comes from the gateway's
// operator telemetry (or over SSH with --ssh), the account half from the chain through the gateway.
package statuscmd

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/display"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/tui"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/operatorview"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/shared"
	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// operatorPrefix is the start of an orama account address.
const operatorPrefix = "orama1"

// --json is a persistent flag on the root, so it is not defined here.
var flags struct {
	env, node, config, operator string
	ssh, once                   bool
	interval                    time.Duration
}

// Cmd is the top-level "status" command.
var Cmd = &cobra.Command{
	Use:   "status",
	Short: "Show your nodes, the cluster, the chain and your account",
	Long: `Show everything about your nodes in one place: each node's cluster health and chain
(height, syncing, validator), the verdict with what to do, and your account on the chain (earnings,
spendable balance, bond): the one 'orama setup' registered your nodes under, or the one --operator names.

In a terminal this is the live view (tab/1-0 switch tabs, ? help, q quit). Piped or with --once it
prints one table; --json prints a document whose "healthy" is true only when the verdict is
operational, every node is healthy and every node's chain answers and has caught up.

The cluster data comes from the gateway's operator telemetry API (sign in with 'orama auth login');
--ssh reads every node over SSH instead, for when no gateway answers. The subcommands show one
aspect at a time.`,
	Args: cobra.NoArgs,
	RunE: run,
}

func init() {
	pf := Cmd.PersistentFlags()
	pf.StringVar(&flags.env, "env", "", "Environment (default: active)")
	pf.StringVar(&flags.node, "node", "", "Show only this node (public IP or WireGuard IP)")
	pf.BoolVar(&flags.ssh, "ssh", false, "Collect over SSH from every node instead of the gateway API (break-glass)")
	pf.StringVar(&flags.config, "config", "", "With --ssh: read nodes from this file instead of resolving them")
	Cmd.Flags().StringVar(&flags.operator, "operator", "", "Your operator account (orama1...), to show its earnings, balance and bond")
	Cmd.Flags().BoolVar(&flags.once, "once", false, "Print one table and exit, even in a terminal")
	Cmd.Flags().DurationVar(&flags.interval, "interval", monitor.DefaultInterval, "How often the live view refreshes")
	for _, v := range Views {
		Cmd.AddCommand(ViewCommand(v, newSource))
	}
}

func run(cmd *cobra.Command, _ []string) error {
	src, err := newSource()
	if err != nil {
		return err
	}
	readOperator, err := operatorReader(operatorToShow(flags.operator, flags.env))
	if err != nil {
		return err
	}
	// The interval is checked even when no live view opens, so a bad value is refused, not ignored.
	interval, err := monitor.ResolveInterval(flags.interval, cmd.Flags().Changed("interval"), flags.ssh)
	if err != nil {
		return err
	}
	jsonMode := printer.For(cmd).JSONMode()
	if !jsonMode && !flags.once && isatty.IsTerminal(os.Stdout.Fd()) {
		return tui.Run(tui.Config{Source: src, Env: flags.env, Interval: interval, Operator: readOperator})
	}
	snap, err := src.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	var op *operatorview.Summary
	if readOperator != nil {
		s := readOperator(cmd.Context())
		op = &s
	}
	if jsonMode {
		return display.StatusJSON(snap, op, os.Stdout)
	}
	return display.StatusTable(snap, op, os.Stdout)
}

// operatorToShow is the account the operator section reads: the one --operator names, else the one
// `orama setup` recorded on the environment, else none.
func operatorToShow(flagged, env string) string {
	if flagged != "" {
		return flagged
	}
	e, err := cli.GetEnvironmentByName(env)
	if err != nil {
		return ""
	}
	return e.Operator
}

// newSource is the snapshot source of the selected environment.
func newSource() (monitor.Source, error) {
	env := flags.env
	if env == "" {
		active, err := cli.GetActiveEnvironment()
		if err != nil {
			return nil, clierr.Usage("no --env given and no active environment: %v", err)
		}
		env = active.Name
	}
	flags.env = env
	return monitor.NewSource(monitor.Options{
		Env: env, Node: flags.node, ConfigPath: flags.config, SSH: flags.ssh, SSHTimeout: monitor.DefaultSSHTimeout,
	})
}

// operatorReader reads address's account through the environment's gateway; nil when no address
// was given.
func operatorReader(address string) (func(context.Context) operatorview.Summary, error) {
	if address == "" {
		return nil, nil
	}
	if !strings.HasPrefix(address, operatorPrefix) || strings.ContainsAny(address, "/?#% ") {
		return nil, clierr.Usage("--operator %q is not an orama address (orama1...)", address)
	}
	gateway, err := shared.GatewayURL("")
	if err != nil {
		return nil, clierr.Usage("no gateway to read the operator account through: %v", err)
	}
	r := &chainread.Reader{Gateway: gateway}
	return func(ctx context.Context) operatorview.Summary {
		return operatorview.Fetch(ctx, r, address, time.Now())
	}, nil
}
