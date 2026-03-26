package buildcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"github.com/spf13/cobra"
)

// Cmd is the top-level build command.
var Cmd = &cobra.Command{
	Use:   "build",
	Short: "Build pre-compiled binary archive for deployment",
	Long: `Cross-compile all Orama binaries and dependencies for Linux,
then package them into a deployment archive. The archive includes:
  - Orama binaries (CLI, node, gateway, identity, SFU, TURN)
  - Olric, IPFS Kubo, IPFS Cluster, RQLite, CoreDNS, Caddy
  - Systemd namespace templates
  - manifest.json with checksums

The resulting archive can be pushed to nodes with 'orama node push'.`,
	Run: func(cmd *cobra.Command, args []string) {
		build.Handle(args)
	},
	DisableFlagParsing: true,
}
