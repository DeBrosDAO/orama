package node

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/production/logs"
	"github.com/DeBrosOfficial/network/pkg/cli/utils"
	"github.com/spf13/cobra"
)

var (
	logsFollow bool
	logsLines  int
	logsSince  string
)

var logsCmd = &cobra.Command{
	Use:   "logs <service>",
	Short: "View production service logs",
	Long: `Stream the journal of one service on this node.

<service> is an alias or a unit name. A tenant service is a systemd template
instance, so name it in full:

  orama node logs orama-namespace-olric@anchat

--since takes a window rather than a line count, which is what a diagnostic
that greps for a periodic line needs:

  orama node logs node --since -30min | grep 'WireGuard peer sync completed'

Aliases: ` + strings.Join(utils.ServiceAliases(), ", "),
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if logsLines < 1 {
			return fmt.Errorf("--lines must be at least 1, got %d", logsLines)
		}
		return logs.Run(args[0], logs.Options{
			Follow:   logsFollow,
			Lines:    logsLines,
			LinesSet: cmd.Flags().Changed("lines"),
			Since:    strings.TrimSpace(logsSince),
		})
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Stream new log lines as they arrive")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", logs.DefaultLines, "How many lines of history to show")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "Show entries newer than this, e.g. -30min or \"2 hours ago\" (overrides --lines)")
}
