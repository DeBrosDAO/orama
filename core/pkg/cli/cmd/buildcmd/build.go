package buildcmd

import (
	"github.com/DeBrosOfficial/network/pkg/cli/build"
	"github.com/spf13/cobra"
)

var buildFlags build.Flags

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
	RunE: func(cmd *cobra.Command, args []string) error {
		return build.Run(&buildFlags)
	},
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&buildFlags.Arch, "arch", "amd64", "Target architecture (amd64, arm64)")
	f.StringVar(&buildFlags.Output, "output", "", "Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz)")
	f.BoolVar(&buildFlags.Verbose, "verbose", false, "Verbose output")
	f.BoolVar(&buildFlags.Sign, "sign", false, "Sign the manifest with rootwallet (requires rw in PATH)")
}
