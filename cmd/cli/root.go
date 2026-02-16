package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	// Command groups
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/app"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/authcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/dbcmd"
	deploycmd "github.com/DeBrosOfficial/network/pkg/cli/cmd/deploy"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/envcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/inspectcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/monitorcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/namespacecmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/node"
)

// version metadata populated via -ldflags at build time
// Must match Makefile: -X 'main.version=...' -X 'main.commit=...' -X 'main.date=...'
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func newRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "orama",
		Short: "Orama CLI - Distributed P2P Network Management Tool",
		Long: `Orama CLI is a tool for managing nodes, deploying applications,
and interacting with the Orama distributed network.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("orama %s", version)
			if commit != "" {
				fmt.Printf(" (commit %s)", commit)
			}
			if date != "" {
				fmt.Printf(" built %s", date)
			}
			fmt.Println()
		},
	})

	// Node operator commands (was "prod")
	rootCmd.AddCommand(node.Cmd)

	// Deploy command (top-level, upsert)
	rootCmd.AddCommand(deploycmd.Cmd)

	// App management (was "deployments")
	rootCmd.AddCommand(app.Cmd)

	// Database commands
	rootCmd.AddCommand(dbcmd.Cmd)

	// Namespace commands
	rootCmd.AddCommand(namespacecmd.Cmd)

	// Environment commands
	rootCmd.AddCommand(envcmd.Cmd)

	// Auth commands
	rootCmd.AddCommand(authcmd.Cmd)

	// Inspect command
	rootCmd.AddCommand(inspectcmd.Cmd)

	// Monitor command
	rootCmd.AddCommand(monitorcmd.Cmd)

	return rootCmd
}

func runCLI() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
