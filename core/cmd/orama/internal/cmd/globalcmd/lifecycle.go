package globalcmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/spf13/cobra"
)

const lifecycleServices = "chain, provider, archiver or repair"

var startCmd = &cobra.Command{
	Use:   "start [service...]",
	Short: "Start the installed global services, chain first (run as root)",
	Long: `Start the installed orama-global-* units, or only the named ones.

The chain starts first. Before it starts, a validator key migrated to this host
is checked against the sign state it last had on its old host; a state behind
it is refused, since it could sign a step the old host already signed. The other
services start once the chain's loopback RPC answers. Starting provider,
archiver or repair alone needs the chain already running.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "starting the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			return l.Start(cmd.Context(), s)
		})
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [service...]",
	Short: "Stop the installed global services, chain last (run as root)",
	Long: `Stop the installed orama-global-* units, or only the named ones, in reverse
start order. Stopping the chain stops every installed service that needs it
first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "stopping the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			return l.Stop(s)
		})
	},
}

var restartCmd = &cobra.Command{
	Use:   "restart [service...]",
	Short: "Restart the installed global services in order (run as root)",
	Long: `Stop then start the named global services (all installed ones when none is
named). Restarting the chain restarts every installed service, chain first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runLifecycle(cmd, args, "restarting the global services", func(l globalnode.Lifecycle, s []install.GlobalService) error {
			return l.Restart(cmd.Context(), s)
		})
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of each installed global service (run as root)",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

func init() {
	Cmd.AddCommand(startCmd, stopCmd, restartCmd, statusCmd)
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

func runStatus(cmd *cobra.Command, _ []string) error {
	if err := clierr.RequireRoot("reading the global services' state"); err != nil {
		return err
	}
	states, err := globalnode.DefaultLifecycle(cmd.OutOrStdout()).Status()
	if err != nil {
		return clierr.Failure("%v", err)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tUNIT\tSTATE")
	for _, s := range states {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Service, s.Unit, s.Active)
	}
	return w.Flush()
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
