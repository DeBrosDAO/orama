package envcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal"
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

var addCAFile string

var addCmd = &cobra.Command{
	Use:   "add <name> <gateway_url> [description]",
	Short: "Add a custom environment",
	Long: `Add a custom environment, or update one already configured.

--ca-file trusts a PEM bundle for this environment's domain and every name
under it, in addition to the system roots: a cluster on Let's Encrypt's
staging CA, or on a private CA. It is not trusted for any other host.`,
	Args: cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.EnvAdd(args, addCAFile)
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
	addCmd.Flags().StringVar(&addCAFile, "ca-file", "", "PEM CA bundle to trust for this environment's domain only")
	Cmd.AddCommand(addCmd)
	Cmd.AddCommand(removeCmd)
}
