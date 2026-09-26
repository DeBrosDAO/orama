package buildcmd

import (
	"github.com/DeBrosOfficial/network/cmd/orama/internal/build"
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
  - manifest.json with checksums of every file, and manifest.sig

The manifest is signed with your RootWallet (the agent's active account, through
its wallet:sign capability). Nodes install only archives signed by an address in
their trust anchor, /etc/orama/archive-signers, so signing is the default;
--unsigned makes an archive for local inspection that no node will install.

--signers rotates the trusted signers: nodes that install this build replace
their list with the given addresses. The build must be signed by a signer the
nodes trust now, and the list must include that signer; retiring a key takes
two builds (the old key adds the new one, the new key then drops the old).

The resulting archive can be pushed to nodes with 'orama node push'.

Examples:
  orama build
  orama build --signers 0xYourWallet,0xNewOperator
  orama build --unsigned --output /tmp/inspect.tar.gz`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return build.Run(&buildFlags)
	},
}

func init() {
	f := Cmd.Flags()
	f.StringVar(&buildFlags.Arch, "arch", "amd64", "Target architecture (amd64, arm64)")
	f.StringVar(&buildFlags.Output, "output", "", "Output archive path (default: /tmp/orama-<version>-linux-<arch>.tar.gz)")
	f.BoolVar(&buildFlags.Verbose, "verbose", false, "Verbose output")
	f.BoolVar(&buildFlags.Unsigned, "unsigned", false, "Do not sign the manifest (a local-only archive: nodes refuse it)")
	f.StringSliceVar(&buildFlags.Signers, "signers", nil,
		"Rotate the trusted archive signers: nodes that install this build trust only these addresses (comma-separated)")

	// Signing is the default now; --sign is accepted so existing scripts keep
	// working, and says so.
	f.Bool("sign", true, "")
	cobra.CheckErr(f.MarkDeprecated("sign", "archives are signed by default; pass --unsigned for a local-only archive"))
}
