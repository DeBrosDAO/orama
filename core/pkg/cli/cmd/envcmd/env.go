package envcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the root command for environment management.
var Cmd = &cobra.Command{
	Use:   "env",
	Short: "Manage environments",
	Long: `List, switch, add, and remove Orama network environments.
Available default environments: production, devnet, testnet.`,
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available environments",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvList()
	},
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show current active environment",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvCurrent()
	},
}

var useCmd = &cobra.Command{
	Use:     "use <name>",
	Aliases: []string{"switch", "enable"},
	Short:   "Switch to a different environment",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvSwitch(args)
	},
}

var addCmd = &cobra.Command{
	Use:   "add <name> <gateway_url> [description]",
	Short: "Add a custom environment",
	Args:  cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvAdd(args)
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvRemove(args)
	},
}

func init() {
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(currentCmd)
	Cmd.AddCommand(useCmd)
	Cmd.AddCommand(addCmd)
	Cmd.AddCommand(removeCmd)
}
