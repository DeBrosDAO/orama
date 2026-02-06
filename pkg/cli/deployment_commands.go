package cli

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli/db"
	"github.com/DeBrosOfficial/network/pkg/cli/deployments"
)

// HandleDeployCommand handles deploy commands
func HandleDeployCommand(args []string) {
	deployCmd := deployments.DeployCmd
	deployCmd.SetArgs(args)

	if err := deployCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// HandleDeploymentsCommand handles deployments management commands
func HandleDeploymentsCommand(args []string) {
	// Create root command for deployments management
	deploymentsCmd := deployments.DeployCmd
	deploymentsCmd.Use = "deployments"
	deploymentsCmd.Short = "Manage deployments"
	deploymentsCmd.Long = "List, get, delete, rollback, and view logs for deployments"

	// Add management subcommands
	deploymentsCmd.AddCommand(deployments.ListCmd)
	deploymentsCmd.AddCommand(deployments.GetCmd)
	deploymentsCmd.AddCommand(deployments.DeleteCmd)
	deploymentsCmd.AddCommand(deployments.RollbackCmd)
	deploymentsCmd.AddCommand(deployments.LogsCmd)
	deploymentsCmd.AddCommand(deployments.StatsCmd)

	deploymentsCmd.SetArgs(args)

	if err := deploymentsCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// HandleDBCommand handles database commands
func HandleDBCommand(args []string) {
	dbCmd := db.DBCmd
	dbCmd.SetArgs(args)

	if err := dbCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
