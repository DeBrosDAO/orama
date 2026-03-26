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
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleEnvCommand([]string{"list"})
	},
}

var currentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show current active environment",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleEnvCommand([]string{"current"})
	},
}

var useCmd = &cobra.Command{
	Use:     "use <name>",
	Aliases: []string{"switch"},
	Short:   "Switch to a different environment",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleEnvCommand(append([]string{"switch"}, args...))
	},
}

var addCmd = &cobra.Command{
	Use:   "add <name> <gateway_url> [description]",
	Short: "Add a custom environment",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleEnvCommand(append([]string{"add"}, args...))
	},
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an environment",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleEnvCommand(append([]string{"remove"}, args...))
	},
}

func init() {
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(currentCmd)
	Cmd.AddCommand(useCmd)
	Cmd.AddCommand(addCmd)
	Cmd.AddCommand(removeCmd)
}
