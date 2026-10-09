package globalcmd

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
	"github.com/spf13/cobra"
)

var monitorFlags struct{ home string }

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Write this relay's or directory authority's monitor.json for the node report (run by orama-global-tor-monitor.timer)",
	Long: `Write <home>/monitor.json with whether the consensus the relay or directory authority
holds lists it: {"in_consensus": true|false}. 'orama monitor node' shows it on the Global
line and the node report raises a warning when the node is not listed. The field is left
out (the file is "{}") while the node has no consensus yet or the one it holds has
expired, so an unknown state is never reported as a no. It reads only the role's own
DataDirectory and writes only monitor.json there.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if monitorFlags.home == "" {
			return clierr.Usage("--home is required: the relay's or directory authority's tor DataDirectory")
		}
		listed, err := tornet.WriteMonitor(monitorFlags.home, time.Now())
		if err != nil {
			return clierr.Failure("%v", err)
		}
		state := "unknown (no valid consensus yet)"
		if listed != nil {
			state = fmt.Sprintf("in_consensus %t", *listed)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s/%s: %s\n", monitorFlags.home, tornet.MonitorFile, state)
		return nil
	},
}

func init() {
	monitorCmd.Flags().StringVar(&monitorFlags.home, "home", "", "The relay's or directory authority's tor DataDirectory [required]")
	torCmd.AddCommand(monitorCmd)
}
