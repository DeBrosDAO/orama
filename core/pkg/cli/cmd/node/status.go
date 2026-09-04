package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/printer"
	"github.com/DeBrosOfficial/network/pkg/cli/production/status"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the service status of the node on this machine",
	Long: `Report the systemd units of the Orama node installed on this machine.

For the health of your whole fleet from your own machine, use 'orama status'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return status.Handle(printer.For(cmd))
	},
}
