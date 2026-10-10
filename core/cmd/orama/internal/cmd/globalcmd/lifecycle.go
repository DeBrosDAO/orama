package globalcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

const lifecycleServices = "chain, ipfs, provider, archiver, indexer, repair, dirauth, relay or onion"

var startCmd = &cobra.Command{
	Use:   "start [service...]",
	Short: "Start the installed global services, chain first (run as root)",
	Long: `Start the installed orama-global-* units, or only the named ones.

The chain starts first. Before it starts, a validator key migrated to this host
is checked against the sign state it last had on its old host; a state behind
it is refused, since it could sign a step the old host already signed. The other
services start once the chain's loopback RPC answers. Starting ipfs, provider,
archiver, indexer or repair alone needs the chain already running. The public
Kubo's GC timer starts and stops with it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "starting the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			warnIfMigratedAway(cmd)
			return l.Start(cmd.Context(), s)
		})
	},
}

// forceAuthorityRoll is --force of stop and restart.
var forceAuthorityRoll bool

const forceAuthorityRollUsage = "Stop or restart a directory authority although another has started less than 30 minutes ago (the network may lose its consensus) or its state cannot be read"

var stopCmd = &cobra.Command{
	Use:   "stop [service...]",
	Short: "Stop the installed global services, chain last (run as root)",
	Long: `Stop the installed orama-global-* units, or only the named ones, in reverse
start order. Stopping the chain stops every installed service that needs it
first.

A directory authority is not stopped while another has started less than 30
minutes ago: a fresh authority casts no Running vote for that long and a
consensus needs two of the three (website/src/docs/operator/tor-network.mdx, "Directory authorities").
--force overrides it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "stopping the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			l.ForceAuthorityRoll = forceAuthorityRoll
			return l.Stop(s)
		})
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart [service...]",
	Short: "Restart the installed global services in order (run as root)",
	Long: `Stop then start the named global services (all installed ones when none is
named). Restarting the chain restarts every installed service, chain first.

A directory authority is restarted one at a time, at least 30 minutes apart:
see "orama global stop". --force overrides the check.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "restarting the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			l.ForceAuthorityRoll = forceAuthorityRoll
			return l.Restart(cmd.Context(), s)
		})
	},
}

func init() {
	stopCmd.Flags().BoolVar(&forceAuthorityRoll, "force", false, forceAuthorityRollUsage)
	restartCmd.Flags().BoolVar(&forceAuthorityRoll, "force", false, forceAuthorityRollUsage)
	Cmd.AddCommand(startCmd, stopCmd, restartCmd)
}

func runLifecycle(cmd *cobra.Command, args []string, what string, act func(globalnode.Lifecycle, []install.GlobalService) error) error {
	services, err := parseServiceArgs(args)
	if err != nil {
		return err
	}
	if err := clierr.RequireRoot(what); err != nil {
		return err
	}
	if err := act(globalnode.DefaultLifecycle(cmd.OutOrStdout()), services); err != nil {
		return clierr.Failure("%v", err)
	}
	return nil
}

// parseServiceArgs names services by their install names.
func parseServiceArgs(args []string) ([]install.GlobalService, error) {
	var out []install.GlobalService
	for _, arg := range args {
		s := install.GlobalService(arg)
		if install.GlobalServiceUnit(s) == "" {
			return nil, clierr.Usage("unknown global service %q (%s)", arg, lifecycleServices)
		}
		out = append(out, s)
	}
	return out, nil
}
