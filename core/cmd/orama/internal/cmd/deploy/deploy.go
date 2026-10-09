package deploy

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/deployments"
	"github.com/spf13/cobra"
)

// Cmd is the top-level deploy command (upsert: create or update).
var Cmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy applications to the Orama network",
	Long: `Deploy static sites, Next.js apps, Go backends, and Node.js backends.
A name that is already deployed in the namespace is refused (409) unless you
redeploy it as an update with --update.`,
}

func init() {
	Cmd.AddCommand(deployments.DeployStaticCmd)
	Cmd.AddCommand(deployments.DeployNextJSCmd)
	Cmd.AddCommand(deployments.DeployGoCmd)
	Cmd.AddCommand(deployments.DeployNodeJSCmd)
}
