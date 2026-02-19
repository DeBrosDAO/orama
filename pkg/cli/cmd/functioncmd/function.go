package functioncmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli/functions"
	"github.com/spf13/cobra"
)

// Cmd is the top-level function command.
var Cmd = &cobra.Command{
	Use:   "function",
	Short: "Manage serverless functions",
	Long: `Deploy, invoke, and manage serverless functions on the Orama Network.

A function is a folder containing:
  function.go    — your handler code (uses the fn SDK)
  function.yaml  — configuration (name, memory, timeout, etc.)

Quick start:
  orama function init my-function
  cd my-function
  orama function build
  orama function deploy
  orama function invoke my-function --data '{"name": "World"}'`,
}

func init() {
	Cmd.AddCommand(functions.InitCmd)
	Cmd.AddCommand(functions.BuildCmd)
	Cmd.AddCommand(functions.DeployCmd)
	Cmd.AddCommand(functions.InvokeCmd)
	Cmd.AddCommand(functions.ListCmd)
	Cmd.AddCommand(functions.GetCmd)
	Cmd.AddCommand(functions.DeleteCmd)
	Cmd.AddCommand(functions.LogsCmd)
	Cmd.AddCommand(functions.VersionsCmd)
}
