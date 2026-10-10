package node

import (
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/invite"
	"github.com/spf13/cobra"
)

var inviteOpts invite.Options

var inviteCmd = &cobra.Command{
	Use:   "invite",
	Short: "Manage invite tokens for joining the cluster",
	Long: `Generate invite tokens that allow new nodes to join the cluster.
Running without a subcommand creates a new token (same as 'invite create').`,
	// RunE, not Run: Run dropped the error invite.Run returns, so a failed
	// mint exited 0 having printed nothing.
	RunE: func(cmd *cobra.Command, args []string) error {
		return invite.Run(inviteOpts)
	},
}

func init() {
	inviteCmd.Flags().DurationVar(&inviteOpts.Expiry, "expiry", time.Hour, "How long the token stays valid")
	inviteCmd.Flags().BoolVar(&inviteOpts.Raw, "raw", false, "Print only the invite, for scripts (orama node setup --join-via reads it this way)")
}
