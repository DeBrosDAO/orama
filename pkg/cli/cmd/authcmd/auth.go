package authcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/spf13/cobra"
)

// Cmd is the root command for authentication.
var Cmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication management",
	Long: `Manage authentication with the Orama network.
Supports RootWallet (EVM) and Phantom (Solana) authentication methods.`,
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with wallet",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand(append([]string{"login"}, args...))
	},
	DisableFlagParsing: true,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored credentials",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand([]string{"logout"})
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current authentication status",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand([]string{"whoami"})
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed authentication info",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand([]string{"status"})
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored credentials",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand([]string{"list"})
	},
}

var switchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Switch between stored credentials",
	Run: func(cmd *cobra.Command, args []string) {
		cli.HandleAuthCommand([]string{"switch"})
	},
}

func init() {
	Cmd.AddCommand(loginCmd)
	Cmd.AddCommand(logoutCmd)
	Cmd.AddCommand(whoamiCmd)
	Cmd.AddCommand(statusCmd)
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(switchCmd)
}
