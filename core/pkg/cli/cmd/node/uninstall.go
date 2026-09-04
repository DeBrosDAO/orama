package node

import (
	"github.com/DeBrosOfficial/network/pkg/cli/production/uninstall"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove production services (requires sudo)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return uninstall.Handle()
	},
}
