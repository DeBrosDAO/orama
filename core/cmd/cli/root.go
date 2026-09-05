package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"github.com/DeBrosOfficial/network/pkg/cli/printer"

	// Command groups
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/app"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/auditcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/authcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/buildcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/dbcmd"
	deploycmd "github.com/DeBrosOfficial/network/pkg/cli/cmd/deploy"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/envcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/functioncmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/inspectcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/invitecmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/memberscmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/monitorcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/namespacecmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/node"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/nodescmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/operatorcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/pushcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/rolloutcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/sandboxcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/sshcmd"
	"github.com/DeBrosOfficial/network/pkg/cli/cmd/statuscmd"
	"github.com/DeBrosOfficial/network/pkg/cli/domain"
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

	printer.Register(rootCmd)

	// Version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), resolveBuildInfo(version, commit, date))
		},
	})

	// Node operator commands (was "prod")
	rootCmd.AddCommand(node.Cmd)

	// Mint an invite for a new node, from here rather than from a node
	rootCmd.AddCommand(invitecmd.Cmd)

	// Deploy command (top-level, upsert)
	rootCmd.AddCommand(deploycmd.Cmd)

	// App management (was "deployments")
	rootCmd.AddCommand(app.Cmd)

	// Database commands
	rootCmd.AddCommand(dbcmd.Cmd)

	// Custom domain commands
	rootCmd.AddCommand(domain.Cmd)

	// Namespace commands
	rootCmd.AddCommand(namespacecmd.Cmd)
	rootCmd.AddCommand(memberscmd.Cmd)

	// Environment commands
	rootCmd.AddCommand(envcmd.Cmd)

	// Auth commands
	rootCmd.AddCommand(authcmd.Cmd)

	// The audit trail
	rootCmd.AddCommand(auditcmd.Cmd)

	// Cluster operations
	rootCmd.AddCommand(operatorcmd.Cmd)

	// Inspect command
	rootCmd.AddCommand(inspectcmd.Cmd)

	// Monitor command
	rootCmd.AddCommand(monitorcmd.Cmd)

	// Serverless function commands
	rootCmd.AddCommand(functioncmd.Cmd)

	// Build command (cross-compile binary archive)
	rootCmd.AddCommand(buildcmd.Cmd)

	// Sandbox command (ephemeral Hetzner Cloud clusters)
	rootCmd.AddCommand(sandboxcmd.Cmd)

	// Unified node management commands
	rootCmd.AddCommand(nodescmd.Cmd)
	rootCmd.AddCommand(pushcmd.Cmd)
	rootCmd.AddCommand(rolloutcmd.Cmd)
	rootCmd.AddCommand(statuscmd.Cmd)
	rootCmd.AddCommand(sshcmd.Cmd)

	classifyUsageErrors(rootCmd)

	return rootCmd
}

// classifyUsageErrors makes cobra's own argument validation exit with the
// usage code instead of the generic failure code.
//
// A caller that can tell "you typed it wrong" from "the cluster refused" can
// act on the difference: the first is never worth retrying, the second may be.
// Only commands that declare an Args validator are wrapped — leaving Args nil
// selects cobra's own default, which is what makes a parent command reject an
// unknown subcommand, and replacing it would make that silently succeed.
func classifyUsageErrors(cmd *cobra.Command) {
	// A group command — subcommands, nothing of its own to run — rejects a
	// positional argument, because the only thing one can be is a subcommand
	// name that does not exist. Cobra returns help and exits zero for a
	// command it considers not runnable, and it decides that before it
	// validates arguments, so `orama node <typo>` printed the group's help and
	// succeeded: a script never learned the subcommand did not exist.
	if cmd.Run == nil && cmd.RunE == nil && cmd.HasSubCommands() {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return c.Help()
			}
			return clierr.Usage("unknown %s subcommand %q\n  Run 'orama %s --help' to see what it takes",
				c.Name(), args[0], c.CommandPath()[len("orama "):])
		}
	}

	if inner := cmd.Args; inner != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			return clierr.Wrap(clierr.CodeUsage, inner(c, args))
		}
	}
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return clierr.Wrap(clierr.CodeUsage, err)
	})
	for _, sub := range cmd.Commands() {
		classifyUsageErrors(sub)
	}
}

// runCLI executes the command tree and turns its error into an exit code.
//
// This is the only place the process exits. Handlers used to call os.Exit
// themselves, which meant deferred cleanup never ran — a push left staged
// private keys behind — and every failure was code 1, so a script could not
// tell a mistyped flag from a cluster that had lost quorum.
func runCLI() {
	rootCmd := newRootCmd()

	// Resolve the command line to a command first. Cobra reports a name it
	// cannot resolve as a plain error, which would come back as the generic
	// failure code while a mistyped subcommand one level down reports a usage
	// error — the same mistake, two different answers to a script.
	if _, _, err := rootCmd.Find(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(clierr.CodeUsage)
	}

	err := rootCmd.Execute()
	if err == nil {
		return
	}

	code := clierr.CodeOf(err)
	if code == clierr.CodeAborted {
		// The operator declined a confirmation. Nothing happened and nothing
		// is wrong, so this is not reported as an error.
		os.Exit(code)
	}

	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(code)
}
