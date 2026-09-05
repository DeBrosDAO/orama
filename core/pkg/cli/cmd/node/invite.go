package node

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/production/invite"
	"github.com/spf13/cobra"
)

var inviteOpts invite.Options

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Manage invite tokens for joining the cluster",
	Long: `Generate invite tokens that allow new nodes to join the cluster.
Running without a subcommand creates a new token (same as 'invite create').`,
	Run: func(cmd *cobra.Command, args []string) {
		// Default behavior: create a new invite token
		invite.Run(inviteOpts)
	},
}

func init() {
	inviteCmd.Flags().DurationVar(&inviteOpts.Expiry, "expiry", time.Hour, "How long the token stays valid")
}
