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

var loginNamespace string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with wallet",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthLogin(loginNamespace)
	},
}

func init() {
	f := loginCmd.Flags()
	f.StringVar(&loginNamespace, "namespace", "", "Namespace name")
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthLogout()
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current authentication status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthWhoami()
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show detailed authentication info",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthStatus()
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthList()
	},
}

var switchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Switch between stored credentials",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cli.AuthSwitch()
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
